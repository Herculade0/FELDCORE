package compose

import (
	"fmt"
	"strings"
)

// Diff is a unified diff of two compose files. An empty result means they
// match. Files of more than about a thousand lines are summarised instead of
// diffed line by line.
func Diff(before, after string) string {
	if before == after {
		return ""
	}
	a := splitLines(before)
	b := splitLines(after)
	if len(a)*len(b) > 1_500_000 {
		return fmt.Sprintf("--- docker-compose.yml\n+++ docker-compose.yml\n@@ file differs (%d lines -> %d lines) @@\n", len(a), len(b))
	}
	return formatUnified(diffOps(a, b))
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

type diffOp struct {
	kind byte
	line string
}

func diffOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		row := dp[i]
		next := dp[i+1]
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				row[j] = next[j+1] + 1
			} else if next[j] >= row[j+1] {
				row[j] = next[j]
			} else {
				row[j] = row[j+1]
			}
		}
	}
	ops := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

func formatUnified(ops []diffOp) string {
	const ctx = 3
	keep := make([]bool, len(ops))
	for i, op := range ops {
		if op.kind == ' ' {
			continue
		}
		from := i - ctx
		if from < 0 {
			from = 0
		}
		to := i + ctx
		if to >= len(ops) {
			to = len(ops) - 1
		}
		for j := from; j <= to; j++ {
			keep[j] = true
		}
	}
	var b strings.Builder
	b.WriteString("--- docker-compose.yml\n+++ docker-compose.yml\n")
	for i := 0; i < len(ops); {
		if !keep[i] {
			i++
			continue
		}
		start := i
		for i < len(ops) && keep[i] {
			i++
		}
		chunk := ops[start:i]
		oldStart, newStart := 1, 1
		for _, op := range ops[:start] {
			if op.kind != '+' {
				oldStart++
			}
			if op.kind != '-' {
				newStart++
			}
		}
		oldN, newN := 0, 0
		for _, op := range chunk {
			if op.kind != '+' {
				oldN++
			}
			if op.kind != '-' {
				newN++
			}
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", oldStart, oldN, newStart, newN)
		for _, op := range chunk {
			b.WriteByte(op.kind)
			b.WriteString(op.line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
