package osupdate

import (
	"regexp"
	"strconv"
	"strings"
)

// compareAPKVersions orders APK versions as apk does for the forms used here:
// dot-separated numbers, an optional letter, _alpha/_beta/_pre/_rc (before the
// release) or _cvs/_svn/_git/_hg/_p (after) suffixes with optional numbers,
// and -rN. Anything else compares as text.
func compareAPKVersions(a, b string) int {
	pa, okA := parseAPKVersion(a)
	pb, okB := parseAPKVersion(b)
	if !okA || !okB {
		return strings.Compare(a, b)
	}
	for i := 0; i < len(pa.numbers) || i < len(pb.numbers); i++ {
		if i >= len(pa.numbers) {
			return -1
		}
		if i >= len(pb.numbers) {
			return 1
		}
		if c := compareInt(pa.numbers[i], pb.numbers[i]); c != 0 {
			return c
		}
	}
	if pa.letter != pb.letter {
		return compareInt(int(pa.letter), int(pb.letter))
	}
	for i := 0; i < len(pa.suffixes) || i < len(pb.suffixes); i++ {
		sa, sb := apkSuffix{}, apkSuffix{}
		if i < len(pa.suffixes) {
			sa = pa.suffixes[i]
		}
		if i < len(pb.suffixes) {
			sb = pb.suffixes[i]
		}
		if c := compareInt(sa.rank, sb.rank); c != 0 {
			return c
		}
		if c := compareInt(sa.number, sb.number); c != 0 {
			return c
		}
	}
	return compareInt(pa.release, pb.release)
}

type apkSuffix struct{ rank, number int }

type apkVersion struct {
	numbers  []int
	letter   byte
	suffixes []apkSuffix
	release  int
}

var suffixRank = map[string]int{"alpha": -4, "beta": -3, "pre": -2, "rc": -1, "cvs": 1, "svn": 2, "git": 3, "hg": 4, "p": 5}

var apkVersionForm = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)*)([a-z]?)((?:_[a-z]+[0-9]*)*)(?:-r([0-9]+))?$`)

func parseAPKVersion(v string) (apkVersion, bool) {
	m := apkVersionForm.FindStringSubmatch(v)
	if m == nil {
		return apkVersion{}, false
	}
	var p apkVersion
	for _, part := range strings.Split(m[1], ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return apkVersion{}, false
		}
		p.numbers = append(p.numbers, n)
	}
	if m[2] != "" {
		p.letter = m[2][0]
	}
	for _, s := range strings.Split(strings.TrimPrefix(m[3], "_"), "_") {
		if s == "" {
			continue
		}
		name := strings.TrimRight(s, "0123456789")
		rank, ok := suffixRank[name]
		if !ok {
			return apkVersion{}, false
		}
		number := 0
		if digits := s[len(name):]; digits != "" {
			number, _ = strconv.Atoi(digits)
		}
		p.suffixes = append(p.suffixes, apkSuffix{rank, number})
	}
	if m[4] != "" {
		p.release, _ = strconv.Atoi(m[4])
	}
	return p, true
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
