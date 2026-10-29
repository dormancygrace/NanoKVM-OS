#!/usr/bin/env python3
"""Latin output head for a PP-OCRv6 recognizer: the multilingual model reads
18 000 characters; keep the CTC blank, ASCII, the Latin letters with
diacritics, common punctuation and the space. The last MatMul (and its bias)
is sliced to those classes, and the dictionary written to match. Fewer classes
make the head smaller and the decoding faster, and the model can no longer
answer with a CJK look-alike of a Latin letter.

  latin_head.py MODEL_DIR IN.onnx OUT.onnx OUT_DICT   (MODEL_DIR holds inference.yml)
"""
import sys

import numpy as np
import onnx
import yaml
from onnx import numpy_helper

model_dir, src, dst, dict_out = sys.argv[1:5]
chars = yaml.safe_load(open(f'{model_dir}/inference.yml'))['PostProcess']['character_dict']
extra = set('–—‘’“”„‚…•·→←↑↓©®™€£¥×°±§«»¿¡')


def wanted(c):
    if len(c) != 1:
        return False
    o = ord(c)
    if 0x21 <= o <= 0x7e or c in extra:
        return True
    return (0xc0 <= o <= 0x24f or 0x1e00 <= o <= 0x1eff) and c.isalpha()


keep = [i for i, c in enumerate(chars) if wanted(c)]
m = onnx.load(src)
g = m.graph
init = {i.name: i for i in g.initializer}
const = {n.output[0]: n for n in g.node if n.op_type == 'Constant'}


def array(name):
    if name in init:
        return numpy_helper.to_array(init[name])
    return numpy_helper.to_array(next(a.t for a in const[name].attribute if a.name == 'value'))


def store(name, value):
    t = numpy_helper.from_array(value.astype(array(name).dtype), name)
    if name in init:
        init[name].CopyFrom(t)
    else:
        for a in const[name].attribute:
            if a.name == 'value':
                a.t.CopyFrom(t)


matmul = [n for n in g.node if n.op_type in ('MatMul', 'Gemm')][-1]
weight = next(x for x in matmul.input if x in init or x in const)
w = array(weight)
classes = w.shape[-1]
# Classes: the CTC blank, the dictionary, the space.
assert classes == len(chars) + 2, (classes, len(chars))
cols = np.array([0] + [i + 1 for i in keep] + [classes - 1])
store(weight, w[..., cols])
bias_add = next((n for n in g.node if n.op_type == 'Add' and matmul.output[0] in n.input), None)
if bias_add is not None:
    bias = next(x for x in bias_add.input if x in init or x in const)
    store(bias, array(bias)[..., cols])
for vi in list(g.output) + list(g.value_info):
    dims = vi.type.tensor_type.shape.dim
    if dims and dims[-1].HasField('dim_value') and dims[-1].dim_value == classes:
        dims[-1].dim_value = len(cols)
onnx.checker.check_model(m)
onnx.save(m, dst)
open(dict_out, 'w', encoding='utf-8').write('\n'.join(chars[i] for i in keep) + '\n')
print(f'{src}: {classes} -> {len(cols)} classes')
