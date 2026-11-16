package proxy

import (
	"bufio"
	"os"
	"sort"
	"strconv"
	"strings"
)

// TCPListeners returns which of ports are in the LISTEN state, from the
// kernel's tables. A missing table returns none.
func TCPListeners(ports ...int) []int {
	want := map[int]bool{}
	for _, p := range ports {
		want[p] = true
	}
	found := map[int]bool{}
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		scanListeners(path, want, found)
	}
	out := make([]int, 0, len(found))
	for p := range found {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

func scanListeners(path string, want, found map[int]bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || fields[3] != "0A" {
			continue
		}
		local := fields[1]
		i := strings.LastIndex(local, ":")
		if i < 0 {
			continue
		}
		n, err := strconv.ParseInt(local[i+1:], 16, 32)
		if err != nil || !want[int(n)] {
			continue
		}
		found[int(n)] = true
	}
}
