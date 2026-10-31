#!/usr/bin/env python3
"""Fold the LearnableAffineBlocks of PP-LCNetV3 (the PP-OCRv5 mobile recognizers)
into the convolutions next to them.

A block is y = s*x + b with two learned scalars, 56 of them in a recognizer
(53 fold; the last ones have no convolution next to them).
tpu-mlir keeps them and the activations around them in BF16 with casts on both
sides, which makes the INT8 model about three times slower. Folded, the network
is the same in float:
  before an activation: Conv -> Add(bias) -> block  =>  Conv(s*W) -> Add(s*bias + b)
  after an activation:  block -> Conv(W, pads)      =>  Pad(-b/s) -> Conv(s*W, bias + b*sum W)
(the explicit pad keeps the border: s*pad + b must be 0).

  fold_affine.py IN.onnx OUT.onnx
"""
import collections
import sys

import numpy as np
import onnx
import onnxruntime
from onnx import helper, numpy_helper

src, dst = sys.argv[1:3]
m = onnx.load(src)
g = m.graph
inits = {i.name: i for i in g.initializer}
const = {n.output[0]: n for n in g.node if n.op_type == 'Constant' and n.attribute[0].name == 'value'}


def value(name):
    if name in inits:
        return numpy_helper.to_array(inits[name])
    if name in const:
        return numpy_helper.to_array(const[name].attribute[0].t)
    return None


def scalar(name):
    v = value(name)
    return v is not None and v.size == 1


producer = {o: n for n in g.node for o in n.output}
users = collections.defaultdict(list)
for n in g.node:
    for i in n.input:
        users[i].append(n)
fresh = iter(range(10 ** 6))


def new_init(arr, base):
    name = f'{base}_fold{next(fresh)}'
    g.initializer.append(numpy_helper.from_array(np.asarray(arr), name))
    inits[name] = g.initializer[-1]
    return name


blocks = []
for n in g.node:
    if n.op_type != 'Mul' or not any(scalar(i) for i in n.input):
        continue
    add = users[n.output[0]]
    if len(add) == 1 and add[0].op_type == 'Add' and any(scalar(i) for i in add[0].input):
        blocks.append((n, add[0]))

drop, rename, before, after = set(), {}, 0, 0
# Two passes: the folds before activations first, while their convolutions are untouched.
for stage, (n, add) in [(0, b) for b in blocks] + [(1, b) for b in blocks]:
    if id(n) in drop:
        continue
    s = float(value(next(i for i in n.input if scalar(i))))
    b = float(value(next(i for i in add.input if scalar(i))))
    x = next(i for i in n.input if not scalar(i))
    p = producer.get(x)
    # Before the activation: x = Conv(...) + bias (a constant, maybe reshaped).
    if stage == 0 and p is not None and p.op_type == 'Add' and len(users[x]) == 1:
        conv = next((producer[i] for i in p.input if i in producer and producer[i].op_type == 'Conv'), None)
        bias_in = next((i for i in p.input if i not in producer or producer[i].op_type != 'Conv'), None)
        if bias_in is not None and bias_in in producer and producer[bias_in].op_type == 'Reshape':
            r = producer[bias_in]
            bias = value(r.input[0]).reshape(value(r.input[1]))
        else:
            bias = value(bias_in) if bias_in else None
        if conv is not None and bias is not None and len(users[conv.output[0]]) == 1 and len(conv.input) == 2:
            conv.input[1] = new_init(value(conv.input[1]) * s, conv.input[1])
            p.input[list(p.input).index(bias_in)] = new_init(bias * s + b, 'bias')
            drop.update({id(n), id(add)})
            rename[add.output[0]] = x
            before += 1
            continue
    # After the activation: the only user is a convolution.
    out_users = users[add.output[0]]
    if stage == 1 and len(out_users) == 1 and out_users[0].op_type == 'Conv' and abs(s) > 1e-6:
        conv = out_users[0]
        attrs = {a.name: helper.get_attribute_value(a) for a in conv.attribute}
        W = value(conv.input[1])
        bias = value(conv.input[2]) if len(conv.input) > 2 else np.zeros(W.shape[0], np.float32)
        conv_in = x
        pads = list(attrs.get('pads', [0, 0, 0, 0]))
        if any(pads):
            conv_in = f'{conv.name}_pad'
            t, left, bottom, right = pads
            g.node.append(helper.make_node(
                'Pad', [x, new_init(np.array([0, 0, t, left, 0, 0, bottom, right], np.int64), 'pads'),
                        new_init(np.array(-b / s, np.float32), 'padval')], [conv_in], name=conv_in))
            for a in conv.attribute:
                if a.name == 'pads':
                    a.ints[:] = [0, 0, 0, 0]
        conv.input[0] = conv_in
        conv.input[1] = new_init((W * s).astype(np.float32), conv.input[1])
        bias_name = new_init((bias + b * W.reshape(W.shape[0], -1).sum(axis=1)).astype(np.float32), 'cbias')
        if len(conv.input) > 2:
            conv.input[2] = bias_name
        else:
            conv.input.append(bias_name)
        drop.update({id(n), id(add)})
        after += 1


def resolve(name):
    while name in rename:
        name = rename[name]
    return name


keep = [k for k in g.node if id(k) not in drop]
for k in keep:
    for j, i in enumerate(k.input):
        k.input[j] = resolve(i)
for o in g.output:
    o.name = resolve(o.name)
# Topological order again: the pads were appended at the end.
order, ready, pending = [], {i.name for i in g.input} | set(inits) | {''}, keep
while pending:
    rest = []
    for k in pending:
        if all(i in ready for i in k.input):
            order.append(k)
            ready.update(k.output)
        else:
            rest.append(k)
    if len(rest) == len(pending):
        sys.exit('fold_affine: graph has a cycle or a missing input')
    pending = rest
del g.node[:]
g.node.extend(order)
onnx.checker.check_model(m)
onnx.save(m, dst)

# The folded network must compute the same in float.
onnxruntime.set_default_logger_severity(3)
x = np.random.default_rng(1).uniform(-1, 1, (1, 3, 48, 320)).astype(np.float32)
a = onnxruntime.InferenceSession(src).run(None, {g.input[0].name: x})[0]
c = onnxruntime.InferenceSession(dst).run(None, {g.input[0].name: x})[0]
diff = float(np.abs(a - c).max())
print(f'{src}: {before} blocks folded before and {after} after activations, '
      f'{len(blocks) - before - after} left (no convolution next to them), max difference {diff:.1e}')
if diff > 1e-3:
    sys.exit(f'fold_affine: the folded network differs by {diff}')
