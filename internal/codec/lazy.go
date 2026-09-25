//go:build !go1.28 && (amd64 || arm64)

// Copyright 2026 The typesafe-sdk-go Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package codec

import (
	"slices"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
)

// lazy is the one second pass that reads structured legend levels as exact
// bytes (plan 6.2.4). The visitor cannot capture a container's bytes, so it
// marks each answer with structured levels, and this pass finds them in
// src: the last "answers" member of the root, the last member of each
// marked name, its last "legend" member, and each level's Raw(), last wins
// throughout, as the visitor resolved the same duplicates. Every lookup
// iterates the members, because Node.Get returns the first match, and
// Properties() unescapes the names, so an escaped "answers" is the
// same member it is to the visitor.
//
// It leaves each marked answer's level bytes in d.raws, from d.rawBase[i]
// on for answer i and indexed like its legend. The bytes are substrings of
// src; intern copies or replaces them.
func (d *decoder) lazy(src string) error {
	v := &d.v
	d.stats.lazyPasses++
	root, err := sonic.GetFromString(src)
	if err != nil {
		return jsonErr(err)
	}
	var p ast.Pair
	it, err := root.Properties()
	if err != nil {
		return jsonErr(err)
	}
	var answers ast.Node
	for it.Next(&p) {
		d.stats.members++
		if p.Key == "answers" {
			answers = p.Value
		}
	}
	d.nodes = growNodes(d.nodes, len(v.set))
	d.raws = d.raws[:0]
	d.rawBase = growInts(d.rawBase, len(v.set))
	it, err = answers.Properties()
	if err != nil {
		return jsonErr(err)
	}
	for it.Next(&p) {
		d.stats.members++
		if i := v.find(p.Key); i >= 0 && v.set[i].structured > 0 {
			d.nodes[i] = p.Value
		}
	}
	for i := range v.set {
		e := &v.set[i]
		if e.structured == 0 || e.err.set {
			continue
		}
		it, err := d.nodes[i].Properties()
		if err != nil {
			return jsonErr(err)
		}
		var legend ast.Node
		for it.Next(&p) {
			d.stats.members++
			if p.Key == "legend" {
				legend = p.Value
			}
		}
		entries := e.ans.Score.Legend
		base := len(d.raws)
		d.rawBase[i] = base
		d.raws = growRaws(d.raws, base+len(entries))
		useMap := len(entries) > linearFold
		if useMap {
			v.levelMap(len(entries))
			for j := range entries {
				v.lvlIdx[entries[j].Level] = j
			}
		}
		lit, err := legend.Properties()
		if err != nil {
			return jsonErr(err)
		}
		for lit.Next(&p) {
			d.stats.members++
			lvl, ok := parseLevel(p.Key)
			if !ok {
				continue // unreachable: the visitor refused such a key
			}
			j := -1
			if useMap {
				if k, ok := v.lvlIdx[lvl]; ok {
					j = k
				}
			} else {
				for k := range entries {
					if entries[k].Level == lvl {
						j = k
						break
					}
				}
			}
			if j < 0 || entries[j].Description.JSON == nil {
				continue // a text level, or a structured level superseded by text
			}
			raw, err := p.Value.Raw()
			if err != nil {
				return jsonErr(err)
			}
			d.raws[base+j] = raw
		}
		for j := range entries {
			if entries[j].Description.JSON != nil && d.raws[base+j] == "" {
				return jsonErr(errLazyMissed) // unreachable: both passes read the same members
			}
		}
	}
	return nil
}

// growNodes returns s with length n and every element zero.
func growNodes(s []ast.Node, n int) []ast.Node {
	if cap(s) < n {
		return make([]ast.Node, n)
	}
	s = s[:n]
	clear(s)
	return s
}

// growInts returns s with length n and every element zero.
func growInts(s []int, n int) []int {
	if cap(s) < n {
		return make([]int, n)
	}
	s = s[:n]
	clear(s)
	return s
}

// growRaws extends s to length n with empty strings, keeping its prefix.
func growRaws(s []string, n int) []string {
	old := len(s)
	s = slices.Grow(s, n-old)[:n]
	clear(s[old:])
	return s
}
