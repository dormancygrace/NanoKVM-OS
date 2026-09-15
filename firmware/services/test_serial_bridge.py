import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("bridge", Path(__file__).with_name("nanokvm-serial-bridge.py"))
bridge = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bridge)

class ParserTest(unittest.TestCase):
    def test_every_split_and_regular_input(self):
        frame = b"\x1b]777;nkos-size;1;" + b"a" * 32 + b";55;244\x07"
        for split in range(len(frame) + 1):
            sizes = []
            parser = bridge.ResizeParser("a" * 32, lambda r,c: sizes.append((r,c)))
            output = parser.feed(b"hello" + frame[:split]) + parser.feed(frame[split:] + b"world")
            self.assertEqual(output, b"helloworld")
            self.assertEqual(sizes, [(55,244)])

    def test_invalid_and_wrong_nonce_passthrough(self):
        for tail in (b";0;80\x07", b";24;9999\x07", b";x;80\x07", b";" + b"9" * 100):
            parser = bridge.ResizeParser("a" * 32, lambda *_: self.fail("invalid resize"))
            data = parser.prefix + tail
            self.assertEqual(parser.feed(data) + parser.flush(), data)
        parser = bridge.ResizeParser("a" * 32, lambda *_: self.fail("wrong nonce"))
        data = b"\x1b]777;nkos-size;1;" + b"b"*32 + b";55;244\x07"
        self.assertEqual(parser.feed(data), data)

    def test_escape_and_binary_passthrough(self):
        parser = bridge.ResizeParser("a"*32, lambda *_: self.fail("not resize"))
        data = bytes(range(256)) + b"\x1b[A\x1b"
        result = b"".join(parser.feed(bytes([b])) for b in data) + parser.flush()
        self.assertEqual(result, data)

if __name__ == '__main__':
    unittest.main()
