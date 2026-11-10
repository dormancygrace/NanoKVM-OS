// hotorder turns a pmuprof profile of a Go program into a function order for
// the linker's -hotorder (see ../README.md):
//
//	go run hotorder.go -binary NanoKVM-Server -profile pmuprof.txt > ../server-webrtc.txt
//
// The profile is the text of /proc/pmuprof (other lines are ignored). A user
// sample is "U tgid pc ra samples": pc names the function that ran, ra its
// caller as long as the function has called nothing yet, which gives call
// edges without unwinding. Functions covering -cover of the program's samples
// are clustered the way hfsort's C3 does: edges by weight, a callee's cluster
// is appended to its caller's while the two stay within -cluster bytes; the
// clusters are laid out by samples per byte. The binary must be the one that
// was profiled, linked as a non-PIE executable (addresses are used as they
// are); its pclntab names the functions, so it may be stripped.
package main

import (
	"bufio"
	"debug/elf"
	"debug/gosym"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

func main() {
	binary := flag.String("binary", "", "the profiled program")
	profile := flag.String("profile", "", "text of /proc/pmuprof")
	comm := flag.String("comm", "NanoKVM-Server", "process name in the profile")
	cover := flag.Float64("cover", 0.97, "share of the samples the listed functions cover")
	limit := flag.Uint64("cluster", 16<<10, "largest cluster in bytes")
	flag.Parse()
	f, err := elf.Open(*binary)
	check(err)
	pcln, err := f.Section(".gopclntab").Data()
	check(err)
	text := f.Section(".text")
	table, err := gosym.NewTable(nil, gosym.NewLineTable(pcln, text.Addr))
	check(err)
	// With external linking the Go text does not start at .text and the
	// table's own start is not relocated: a Go function that is also a
	// dynamic symbol gives the true start.
	dynamic, _ := f.DynamicSymbols()
	for _, d := range dynamic {
		if fn := table.LookupFunc(d.Name); fn != nil && d.Value != 0 {
			table, err = gosym.NewTable(nil, gosym.NewLineTable(pcln, text.Addr+d.Value-fn.Entry))
			check(err)
			break
		}
	}

	in, err := os.Open(*profile)
	check(err)
	var users [][]string
	processes := map[string]bool{}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		w := strings.Fields(scanner.Text())
		switch {
		case len(w) >= 9 && w[0] == "P" && w[2] == "u" && strings.Join(w[3:len(w)-5], " ") == *comm:
			processes[w[1]] = true
		case len(w) == 5 && w[0] == "U":
			users = append(users, w)
		}
	}
	samples, sizes := map[string]int{}, map[string]uint64{}
	edges := map[[2]string]int{} // caller, callee
	total := 0
	function := func(s string) *gosym.Func {
		pc, err := strconv.ParseUint(s, 16, 64)
		if err != nil {
			return nil
		}
		fn := table.PCToFunc(pc)
		// A space would end the name in the order file; a trampoline is the linker's own.
		if fn == nil || strings.Contains(fn.Name, " ") || strings.Contains(fn.Name, "-tramp") {
			return nil
		}
		sizes[fn.Name] = fn.End - fn.Entry
		return fn
	}
	for _, w := range users {
		n, _ := strconv.Atoi(w[4])
		if !processes[w[1]] {
			continue
		}
		fn := function(w[2])
		if fn == nil {
			continue
		}
		total += n
		samples[fn.Name] += n
		if caller := function(w[3]); caller != nil && caller.Name != fn.Name {
			edges[[2]string{caller.Name, fn.Name}] += n
		}
	}
	if total == 0 {
		check(fmt.Errorf("no user samples of %q in the profile", *comm))
	}

	names := make([]string, 0, len(samples))
	for name := range samples {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := names[i], names[j]
		return samples[a] > samples[b] || samples[a] == samples[b] && a < b
	})
	cluster, of := map[string][]string{}, map[string]string{}
	covered := 0
	for _, name := range names {
		if float64(covered) >= *cover*float64(total) {
			break
		}
		cluster[name], of[name] = []string{name}, name
		covered += samples[name]
	}
	size := func(rep string) (n uint64) {
		for _, m := range cluster[rep] {
			n += sizes[m]
		}
		return n
	}
	weight := func(rep string) (n int) {
		for _, m := range cluster[rep] {
			n += samples[m]
		}
		return n
	}
	type edge struct {
		caller, callee string
		n              int
	}
	list := make([]edge, 0, len(edges))
	for k, n := range edges {
		list = append(list, edge{k[0], k[1], n})
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.n != b.n {
			return a.n > b.n
		}
		return a.callee < b.callee || a.callee == b.callee && a.caller < b.caller
	})
	for _, e := range list {
		a, hot := of[e.caller]
		b, alsoHot := of[e.callee]
		// A callee joins only as the head of its cluster: it then follows its caller's code.
		if !hot || !alsoHot || a == b || size(a)+size(b) > *limit || cluster[b][0] != e.callee {
			continue
		}
		for _, m := range cluster[b] {
			of[m] = a
		}
		cluster[a] = append(cluster[a], cluster[b]...)
		delete(cluster, b)
	}
	reps := make([]string, 0, len(cluster))
	for rep := range cluster {
		reps = append(reps, rep)
	}
	sort.Slice(reps, func(i, j int) bool {
		a, b := reps[i], reps[j]
		da, db := float64(weight(a))/float64(max(size(a), 1)), float64(weight(b))/float64(max(size(b), 1))
		return da > db || da == db && a < b
	})
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	count, code := 0, uint64(0)
	for _, rep := range reps {
		for _, m := range cluster[rep] {
			fmt.Fprintln(out, m)
			count++
			code += sizes[m]
		}
	}
	fmt.Fprintf(os.Stderr, "%d functions, %d KiB, %.1f%% of %d samples, %d clusters\n",
		count, code>>10, 100*float64(covered)/float64(total), total, len(reps))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "hotorder:", err)
		os.Exit(1)
	}
}
