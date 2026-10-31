# SPDX-License-Identifier: GPL-3.0-or-later
"""Train the screen-state classifier on synthetic screens (synth.py).

    python3 train.py TRAIN VALID OUT.onnx

TRAIN and VALID are synth.py outputs. The model takes the picture as B, G
and R planes scaled to 0..1 (the TPU does the scaling, see convert.sh) and
gives one score per class of synth.CLASSES; the helper applies softmax.
Small on purpose: about 0.4 M weights, well under a millisecond on the TPU.
"""
import os
import sys

import numpy as np
import torch
import torch.nn as nn
from PIL import Image

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from synth import CLASSES, INPUT  # noqa: E402

torch.manual_seed(1)
np.random.seed(1)
torch.set_num_threads(os.cpu_count() or 4)


def load(root):
    xs, ys = [], []
    for label, name in enumerate(CLASSES):
        folder = os.path.join(root, name)
        for f in sorted(os.listdir(folder)):
            rgb = np.asarray(Image.open(os.path.join(folder, f)).convert('RGB'))
            xs.append(rgb[:, :, ::-1].transpose(2, 0, 1).copy())  # B, G, R planes
            ys.append(label)
    return np.stack(xs), np.array(ys, dtype=np.int64)


def block(cin, cout, stride):
    return [nn.Conv2d(cin, cout, 3, stride, 1, bias=False), nn.BatchNorm2d(cout), nn.ReLU(inplace=True)]


class ScreenNet(nn.Module):
    def __init__(self):
        super().__init__()
        self.features = nn.Sequential(
            *block(3, 16, 2),    # 64 x 112
            *block(16, 32, 2),   # 32 x 56
            *block(32, 32, 1),
            *block(32, 64, 2),   # 16 x 28
            *block(64, 64, 1),
            *block(64, 96, 2),   # 8 x 14
            *block(96, 128, 2),  # 4 x 7: where on the screen still counts
        )
        self.head = nn.Sequential(nn.Flatten(), nn.Dropout(0.3), nn.Linear(128 * 4 * 7, 64),
                                  nn.ReLU(inplace=True), nn.Linear(64, len(CLASSES)))

    def forward(self, x):
        return self.head(self.features(x))


def batches(x, y, size, shuffle):
    order = np.random.permutation(len(x)) if shuffle else np.arange(len(x))
    for i in range(0, len(x), size):
        idx = order[i:i + size]
        xb = torch.from_numpy(x[idx]).float() / 255.0
        if shuffle:  # a little more variety in level and colour than synth.py gives
            xb = xb * torch.empty(len(idx), 3, 1, 1).uniform_(0.9, 1.1) + torch.empty(len(idx), 1, 1, 1).uniform_(-0.04, 0.04)
            xb = xb.clamp(0, 1)
        yield xb, torch.from_numpy(y[idx])


def evaluate(model, x, y):
    model.eval()
    predictions = []
    with torch.no_grad():
        for xb, _ in batches(x, y, 512, False):
            predictions.append(model(xb).argmax(1).numpy())
    p = np.concatenate(predictions)
    confusion = np.zeros((len(CLASSES), len(CLASSES)), dtype=np.int64)
    for t, q in zip(y, p):
        confusion[t, q] += 1
    return (p == y).mean(), confusion


def main():
    train_dir, valid_dir, out = sys.argv[1:4]
    xt, yt = load(train_dir)
    xv, yv = load(valid_dir)
    print('train', xt.shape, np.bincount(yt), 'valid', xv.shape, np.bincount(yv), flush=True)
    model = ScreenNet()
    print('weights', sum(p.numel() for p in model.parameters()), flush=True)
    epochs = int(os.environ.get('EPOCHS', '12'))
    optimizer = torch.optim.AdamW(model.parameters(), lr=2e-3, weight_decay=1e-4)
    schedule = torch.optim.lr_scheduler.OneCycleLR(optimizer, max_lr=3e-3, epochs=epochs,
                                                   steps_per_epoch=(len(xt) + 127) // 128)
    # The classes are drawn unevenly (synth.WEIGHTS): weigh the loss back.
    counts = np.bincount(yt, minlength=len(CLASSES))
    loss_fn = nn.CrossEntropyLoss(weight=torch.tensor(counts.sum() / (len(CLASSES) * counts), dtype=torch.float32),
                                  label_smoothing=0.05)
    for epoch in range(epochs):
        model.train()
        total = 0.0
        for xb, yb in batches(xt, yt, 128, True):
            optimizer.zero_grad()
            loss = loss_fn(model(xb), yb)
            loss.backward()
            optimizer.step()
            schedule.step()
            total += loss.item() * len(xb)
        accuracy, _ = evaluate(model, xv, yv)
        print(f'epoch {epoch + 1}: loss {total / len(xt):.4f} valid {accuracy:.4f}', flush=True)
    accuracy, confusion = evaluate(model, xv, yv)
    print('valid accuracy', accuracy)
    print('rows: true class, columns: predicted;', ' '.join(CLASSES))
    for name, row in zip(CLASSES, confusion):
        print(f'{name:>9}', ' '.join(f'{v:6d}' for v in row))
    model.eval()
    torch.onnx.export(model, torch.zeros(1, 3, INPUT[1], INPUT[0]), out, input_names=['input'],
                      output_names=['scores'], opset_version=13, do_constant_folding=True)
    print('wrote', out)


if __name__ == '__main__':
    main()
