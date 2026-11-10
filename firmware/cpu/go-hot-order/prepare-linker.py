#!/usr/bin/env python3
"""Give the Go linker of a prepared GOROOT the option -hotorder=FILE.

The functions named in FILE (one symbol per line) are laid out first in the
text, in that order; everything else keeps its order after them. See
README.md for why.

    prepare-linker.py --goroot PREPARED

PREPARED is a GOROOT made by ../sysmon-runtime/prepare.py: a tree of symbolic
links into the base toolchain. This script replaces the linked directories on
the way to the two linker sources and to the linker binary by directories of
links, edits copies of the sources and rebuilds cmd/link into the prepared
tree. The base toolchain is not written to.
"""
from pathlib import Path
import argparse
import os
import shutil
import subprocess

parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
parser.add_argument('--goroot', type=Path, required=True)
args = parser.parse_args()
goroot = args.goroot.absolute()


def own(root, rel):
    """Make root/rel a directory of the prepared tree: every linked directory
    on the way becomes a directory of links to what it held."""
    path = root
    for part in Path(rel).parts:
        path = path / part
        if path.is_symlink():
            target = path.resolve()
            path.unlink()
            path.mkdir()
            for child in target.iterdir():
                (path / child.name).symlink_to(child, target_is_directory=child.is_dir())
    return path


def edit(path, old, new, count=1):
    if path.is_symlink():
        text = path.read_text()
        path.unlink()
    else:
        text = path.read_text()
    if text.count(old) != count:
        parser.error(f'{path.name}: expected {count} match(es) of {old!r}')
    path.write_text(text.replace(old, new))


ld = own(goroot / 'src', 'cmd/link/internal/ld')
edit(ld / 'main.go', '''	flagRandLayout    = flag.Int64("randlayout", 0, "randomize function layout")
''', '''	flagRandLayout    = flag.Int64("randlayout", 0, "randomize function layout")
	flagHotOrder      = flag.String("hotorder", "", "lay out the functions listed in `file` first, in its order")
''')
# The functions of a package are no longer neighbours: calls between them may
# need a trampoline, as with a random layout.
edit(ld / 'data.go', '&& *flagRandLayout == 0 {', '&& *flagRandLayout == 0 && *flagHotOrder == "" {', 2)
edit(ld / 'data.go', '''	// Sort the text symbols by type, so that FIPS symbols are
''', '''	if *flagHotOrder != "" {
		list, err := os.ReadFile(*flagHotOrder)
		if err != nil {
			Exitf("hotorder: %v", err)
		}
		rank := make(map[string]int)
		for i, name := range strings.Fields(string(list)) {
			if _, ok := rank[name]; !ok {
				rank[name] = i + 1
			}
		}
		textp := ctxt.Textp
		i := 0
		if len(textp) > 0 && ldr.SymName(textp[0]) == "go:buildid" {
			i++
		}
		// C symbols stay where they are, as for -randlayout.
		for i < len(textp) && (ldr.SubSym(textp[i]) != 0 || ldr.AttrSubSymbol(textp[i])) {
			i++
		}
		textp = textp[i:]
		sort.SliceStable(textp, func(a, b int) bool {
			ra, rb := rank[ldr.SymName(textp[a])], rank[ldr.SymName(textp[b])]
			if ra == 0 {
				return false
			}
			return rb == 0 || ra < rb
		})
	}

	// Sort the text symbols by type, so that FIPS symbols are
''')

go = str(goroot / 'bin/go')
env = dict(os.environ, GOROOT=str(goroot), GOTOOLCHAIN='local', GOFLAGS='', GOOS='', GOARCH='')
host = subprocess.check_output([go, 'env', 'GOHOSTOS', 'GOHOSTARCH'], env=env, text=True).split()
tools = own(goroot, f'pkg/tool/{host[0]}_{host[1]}')
linker = tools / 'link'
# go install links the new linker with the old one and would write through
# the link into the base toolchain: give the prepared tree a copy to replace.
base_linker = linker.resolve()
linker.unlink()
shutil.copy2(base_linker, linker)
subprocess.run([go, 'install', 'cmd/link'], env=env, cwd=goroot / 'src', check=True)
if linker.is_symlink() or 'hotorder' not in subprocess.run(
        [str(linker), '-help'], capture_output=True, text=True).stderr:
    parser.error('the rebuilt linker is not in the prepared tree or has no -hotorder')
print(f'{linker}: -hotorder available')
