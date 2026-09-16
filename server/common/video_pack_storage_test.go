package common

import "testing"

func TestVideoPackStorageOwnership(t *testing.T) {
	s := videoPackStorage{headroom: 9}
	a, b := []byte{1, 2}, []byte{3, 4, 5}
	if !s.appendPack(a, 0, 5) || !s.appendPack(b, 2, 5) || !s.complete() {
		t.Fatal("assembly")
	}
	a[0] = 99
	b[0] = 99
	for i, v := range []byte{1, 2, 3, 4, 5} {
		if s.storage[9+i] != v {
			t.Fatal("borrowed memory escaped")
		}
	}
	for _, v := range s.storage[:9] {
		if v != 0 {
			t.Fatal("headroom overwritten")
		}
	}
}
func TestVideoPackStorageRejectsMalformed(t *testing.T) {
	for _, f := range []func(*videoPackStorage) bool{
		func(s *videoPackStorage) bool { return s.appendPack([]byte{1}, 1, 2) },
		func(s *videoPackStorage) bool { return s.appendPack([]byte{1, 2}, 0, 1) },
		func(s *videoPackStorage) bool { return s.appendPack(nil, 0, 1) },
		func(s *videoPackStorage) bool { return s.appendPack([]byte{1}, 0, 65<<20) },
		func(s *videoPackStorage) bool { s.appendPack([]byte{1}, 0, 2); return s.appendPack([]byte{2}, 1, 3) },
	} {
		s := videoPackStorage{}
		if f(&s) || s.complete() {
			t.Fatal("accepted malformed frame")
		}
		if s.appendPack([]byte{1}, 0, 1) {
			t.Fatal("failed frame revived")
		}
	}
	s := videoPackStorage{}
	s.appendPack([]byte{1}, 0, 2)
	if s.complete() {
		t.Fatal("partial frame complete")
	}
}
