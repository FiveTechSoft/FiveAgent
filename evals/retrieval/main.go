// Command retrieval is a local baseline for document retrieval: BM25 over
// heading-sized chunks of this repository's own docs, scored against hand
// labeled queries. It uses no network and no model. Vector and hybrid
// variants are not implemented here yet.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"
)

type chunk struct {
	file, heading, text string
	toks                []string
}

type query struct {
	Q string `json:"q"`
	F string `json:"f"`
	H string `json:"h"`
}

var fold = strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n")

var stop = map[string]bool{}

func init() {
	for _, w := range strings.Fields("the a an and or of to in on for is are with how do does can i my it at by from that this what which when who el la los las un una y o de del en para es son con como que se por mi lo al") {
		stop[w] = true
	}
}

func tokens(s string, useStop bool) []string {
	s = fold.Replace(strings.ToLower(s))
	f := strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
	out := f[:0]
	for _, w := range f {
		if useStop && stop[w] {
			continue
		}
		out = append(out, w)
	}
	return out
}

// load splits each file at ## and ### headings; sections over maxWords
// are split at blank lines and keep their heading.
func load(root string, files []string, maxWords int) []chunk {
	var cs []chunk
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			panic(err)
		}
		heading := "(top)"
		var buf []string
		inFence := false
		flush := func() {
			text := strings.TrimSpace(strings.Join(buf, "\n"))
			buf = nil
			if text == "" {
				return
			}
			words := strings.Fields(text)
			if len(words) <= maxWords {
				cs = append(cs, chunk{file: f, heading: heading, text: text})
				return
			}
			var cur []string
			n := 0
			for _, para := range strings.Split(text, "\n\n") {
				pw := len(strings.Fields(para))
				if n+pw > maxWords && n > 0 {
					cs = append(cs, chunk{file: f, heading: heading, text: strings.Join(cur, "\n\n")})
					cur, n = nil, 0
				}
				cur = append(cur, para)
				n += pw
			}
			if len(cur) > 0 {
				cs = append(cs, chunk{file: f, heading: heading, text: strings.Join(cur, "\n\n")})
			}
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "```") {
				inFence = !inFence
			}
			if !inFence && (strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") || strings.HasPrefix(line, "# ")) {
				flush()
				heading = strings.TrimSpace(strings.TrimLeft(line, "# "))
			}
			buf = append(buf, line)
		}
		flush()
	}
	return cs
}

type index struct {
	cs    []chunk
	df    map[string]int
	avgdl float64
}

func build(cs []chunk, useStop bool) *index {
	ix := &index{cs: cs, df: map[string]int{}}
	total := 0
	for i := range cs {
		cs[i].toks = tokens(cs[i].heading+" "+cs[i].text, useStop)
		total += len(cs[i].toks)
		seen := map[string]bool{}
		for _, t := range cs[i].toks {
			if !seen[t] {
				seen[t] = true
				ix.df[t]++
			}
		}
	}
	ix.avgdl = float64(total) / float64(len(cs))
	return ix
}

func (ix *index) search(q string, useStop bool, k int) []int {
	const k1, b = 1.2, 0.75
	qt := tokens(q, useStop)
	n := float64(len(ix.cs))
	type sc struct {
		i int
		s float64
	}
	var all []sc
	for i, c := range ix.cs {
		tf := map[string]int{}
		for _, t := range c.toks {
			tf[t]++
		}
		s := 0.0
		for _, t := range qt {
			f := float64(tf[t])
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(ix.df[t])+0.5)/(float64(ix.df[t])+0.5))
			s += idf * f * (k1 + 1) / (f + k1*(1-b+b*float64(len(c.toks))/ix.avgdl))
		}
		all = append(all, sc{i, s})
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].s > all[b].s })
	var out []int
	for _, x := range all[:min(k, len(all))] {
		out = append(out, x.i)
	}
	return out
}

func main() {
	root := flag.String("root", ".", "repository root")
	qfile := flag.String("queries", "evals/retrieval/queries.json", "labeled queries")
	maxw := flag.Int("chunk-words", 180, "max words per chunk")
	verbose := flag.Bool("v", false, "list misses")
	flag.Parse()
	files := []string{"docs/ROADMAP.md", "docs/choosing-a-model.md", "docs/deploy-linux.md", "docs/design.md", "docs/integrations.md", "docs/mcp.md", "docs/memory-training.md", "docs/telegram.md", "docs/whatsapp.md", "skills/fivetech/SKILL.md"}
	raw, err := os.ReadFile(filepath.Join(*root, *qfile))
	if err != nil {
		panic(err)
	}
	var qs []query
	if err := json.Unmarshal(raw, &qs); err != nil {
		panic(err)
	}
	for _, useStop := range []bool{false, true} {
		var m0, m1 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m0)
		t0 := time.Now()
		cs := load(*root, files, *maxw)
		ix := build(cs, useStop)
		idxTime := time.Since(t0)
		runtime.GC()
		runtime.ReadMemStats(&m1)
		hit5, hit1 := 0, 0
		mrr := 0.0
		var lat []time.Duration
		for _, q := range qs {
			t := time.Now()
			top := ix.search(q.Q, useStop, 5)
			lat = append(lat, time.Since(t))
			rank := 0
			for r, i := range top {
				c := ix.cs[i]
				if c.file == q.F && strings.HasPrefix(c.heading, q.H) {
					rank = r + 1
					break
				}
			}
			if rank > 0 {
				hit5++
				mrr += 1 / float64(rank)
			}
			if rank == 1 {
				hit1++
			}
			if *verbose && rank != 1 {
				fmt.Printf("  rank=%d  %q -> want %s#%s; got %s#%s\n", rank, q.Q, q.F, q.H, ix.cs[top[0]].file, ix.cs[top[0]].heading)
			}
		}
		sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
		n := float64(len(qs))
		fmt.Printf("bm25 stopwords=%v chunks=%d queries=%d recall@1=%.3f recall@5=%.3f MRR=%.3f p50=%v p95=%v index=%v heap_delta=%dKB\n",
			useStop, len(cs), len(qs), float64(hit1)/n, float64(hit5)/n, mrr/n,
			lat[len(lat)/2], lat[int(float64(len(lat))*0.95)], idxTime.Round(time.Millisecond), (int64(m1.HeapAlloc)-int64(m0.HeapAlloc))/1024)
	}
}
