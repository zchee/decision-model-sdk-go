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

import "github.com/zchee/typesafe-sdk-go/internal/wire"

// structuredMark marks a legend level whose bytes the lazy pass finds. It is
// non-nil and empty, which no level the decoder keeps can be.
var structuredMark = make([]byte, 0)

// linearFold is the number of entries up to which a fold scans what it has
// kept instead of hashing the key. Past it, a scratch map keeps a legend or
// a probability list of 10^4 entries linear in time (AC-P8).
const linearFold = 16

// linearSet is the number of answers up to which the answer set is searched
// by scanning; past it the set keeps a scratch index, as [wire.Answers] does.
const linearSet = 8

// commitAnswer ends an answer object. Its type decides which members it
// needs; the first member in schema order that is of the wrong kind,
// missing or holding a bad entry is the failure its last occurrence
// reports, as pydantic reports it. Its value, or its failure, replaces any
// earlier answer of the same name, which keeps its position.
func (v *visitor) commitAnswer() {
	a := &v.cur
	e := entry{name: a.name}
	switch {
	case a.wrong&mType != 0:
		e.err = answerPend(a.name, "type", errNotString)
	case a.has&mType == 0:
		e.err = answerPend(a.name, "type", errMissing)
	default:
		e.ans.Kind = wire.ParseKind(a.typ)
	}
	if e.err.set || e.ans.Kind == wire.KindUnknown {
		e.typ = a.typ
		v.put(e)
		return
	}
	for _, m := range schema[e.ans.Kind] {
		switch {
		case a.wrong&m.bit != 0:
			e.err = answerPend(a.name, m.name, wrongKindErr(m.bit))
		case a.has&m.bit == 0:
			e.err = answerPend(a.name, m.name, errMissing)
		case m.bit == mLegend:
			e.err = v.foldLegend(&e)
		case m.bit == mProbs && e.ans.Kind == wire.KindChoice:
			e.err = v.foldLabels(&e)
		case m.bit == mProbs:
			e.err = v.foldLevels(&e)
		}
		if e.err.set {
			e.ans = wire.Answer{}
			e.structured = 0
			v.put(e)
			return
		}
	}
	switch e.ans.Kind {
	case wire.KindNoul:
		e.ans.Noul.Noul = a.noul
	case wire.KindChoice:
		e.ans.Choice.Choice, e.ans.Choice.Confidence = a.choice, a.conf
	case wire.KindScore:
		e.ans.Score.Score, e.ans.Score.Confidence = a.score, a.conf
	}
	v.put(e)
}

// wrongKindErr is the reason for a member of the wrong kind.
func wrongKindErr(bit uint16) error {
	switch bit {
	case mChoice, mType:
		return errNotString
	case mProbs, mLegend:
		return errNotObject
	default:
		return errNotNumber
	}
}

// firstFailure returns the wire position of the first failure of a folded
// member, or -1: the first key that failed to parse (badKey, or -1) or the
// first level or label whose last value is bad, at the position where it
// first appeared. folds is in first-position order.
func firstFailure(badKey int, folds []fold) (pos int, isKey bool) {
	pos, isKey = badKey, badKey >= 0
	for _, f := range folds {
		if f.bad {
			if pos < 0 || f.first < pos {
				return f.first, false
			}
			break
		}
	}
	return pos, isKey
}

// foldLegend builds a score answer's legend from the levels the traversal
// met: one entry per level, at the position of its first occurrence, with
// the value of its last (Python's dict, keyed by the parsed level). A
// structured level holds structuredMark until the lazy pass. Keys that spell
// the same level differently ("1" and "01") fold into one level in wire
// order; the Python SDK folds equal spellings first, which differs only when
// both kinds of repeat meet in one legend.
func (v *visitor) foldLegend(e *entry) pend {
	n := len(v.legend)
	legend := make([]wire.LegendEntry, 0, n)
	folds := v.folds[:0]
	useMap := n > linearFold
	if useMap {
		v.levelMap(n)
	}
	badKey := -1
	for i, l := range v.legend {
		lvl, ok := parseLevel(l.key)
		if !ok {
			if badKey < 0 {
				badKey = i
			}
			continue
		}
		desc := wire.Content{Text: l.text}
		if l.structured {
			desc = wire.Content{JSON: structuredMark}
		}
		j := -1
		if useMap {
			if k, ok := v.lvlIdx[lvl]; ok {
				j = k
			}
		} else {
			for k := range legend {
				if legend[k].Level == lvl {
					j = k
					break
				}
			}
		}
		if j >= 0 {
			legend[j].Description = desc
			folds[j].bad = l.bad
			continue
		}
		if useMap {
			v.lvlIdx[lvl] = len(legend)
		}
		legend = append(legend, wire.LegendEntry{Level: lvl, Description: desc})
		folds = append(folds, fold{first: i, bad: l.bad})
	}
	v.folds = folds
	if pos, isKey := firstFailure(badKey, folds); pos >= 0 {
		if isKey {
			return keyPend(e.name, "legend", v.legend[pos].key, errNotLevel)
		}
		return keyPend(e.name, "legend", v.legend[pos].key, errLegendValue)
	}
	for i := range legend {
		if legend[i].Description.JSON != nil {
			e.structured++
		}
	}
	e.ans.Score.Legend = legend
	return pend{}
}

// foldLevels builds a score answer's probabilities the way foldLegend builds
// its legend.
func (v *visitor) foldLevels(e *entry) pend {
	n := len(v.probs)
	probs := make([]wire.LevelProbability, 0, n)
	folds := v.folds[:0]
	useMap := n > linearFold
	if useMap {
		v.levelMap(n)
	}
	badKey := -1
	for i, p := range v.probs {
		lvl, ok := parseLevel(p.key)
		if !ok {
			if badKey < 0 {
				badKey = i
			}
			continue
		}
		j := -1
		if useMap {
			if k, ok := v.lvlIdx[lvl]; ok {
				j = k
			}
		} else {
			for k := range probs {
				if probs[k].Level == lvl {
					j = k
					break
				}
			}
		}
		if j >= 0 {
			probs[j].Probability = p.p
			folds[j].bad = p.bad
			continue
		}
		if useMap {
			v.lvlIdx[lvl] = len(probs)
		}
		probs = append(probs, wire.LevelProbability{Level: lvl, Probability: p.p})
		folds = append(folds, fold{first: i, bad: p.bad})
	}
	v.folds = folds
	if pos, isKey := firstFailure(badKey, folds); pos >= 0 {
		if isKey {
			return keyPend(e.name, "probabilities", v.probs[pos].key, errNotLevel)
		}
		return keyPend(e.name, "probabilities", v.probs[pos].key, errNotNumber)
	}
	e.ans.Score.Probabilities = probs
	return pend{}
}

// foldLabels builds a choice answer's probabilities: one entry per label, at
// the position of its first occurrence, with the value of its last.
func (v *visitor) foldLabels(e *entry) pend {
	n := len(v.probs)
	probs := make([]wire.LabelProbability, 0, n)
	folds := v.folds[:0]
	useMap := n > linearFold
	if useMap {
		if v.strIdx == nil {
			v.strIdx = make(map[string]int, n)
		}
		clear(v.strIdx)
	}
	for i, p := range v.probs {
		j := -1
		if useMap {
			if k, ok := v.strIdx[p.key]; ok {
				j = k
			}
		} else {
			for k := range probs {
				if probs[k].Label == p.key {
					j = k
					break
				}
			}
		}
		if j >= 0 {
			probs[j].Probability = p.p
			folds[j].bad = p.bad
			continue
		}
		if useMap {
			v.strIdx[p.key] = len(probs)
		}
		probs = append(probs, wire.LabelProbability{Label: p.key, Probability: p.p})
		folds = append(folds, fold{first: i, bad: p.bad})
	}
	v.folds = folds
	if pos, _ := firstFailure(-1, folds); pos >= 0 {
		return keyPend(e.name, "probabilities", v.probs[pos].key, errNotNumber)
	}
	e.ans.Choice.Probabilities = probs
	return pend{}
}

// levelMap readies the scratch level index for n levels.
func (v *visitor) levelMap(n int) {
	if v.lvlIdx == nil {
		v.lvlIdx = make(map[uint32]int, n)
	}
	clear(v.lvlIdx)
}

// parseLevel parses a level key with the grammar [+]?[0-9]+ into a uint32
// (plan 6.2.6): "1", "+1", "01" and "+01" are level 1; a sign other than a
// leading "+", a space, an underscore, a point, an empty key and a value
// past 2^32-1 fail. pydantic's lax int takes more (Appendix B).
func parseLevel(key string) (uint32, bool) {
	s := key
	if len(s) > 0 && s[0] == '+' {
		s = s[1:]
	}
	if s == "" {
		return 0, false
	}
	var n uint64
	for i := range len(s) {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + uint64(c-'0')
		if n > 1<<32-1 {
			return 0, false
		}
	}
	return uint32(n), true
}

// put stores e under its name: a repeated name keeps its first position and
// takes the last value, as a Python dict does.
func (v *visitor) put(e entry) {
	if i := v.find(e.name); i >= 0 {
		v.set[i] = e
		return
	}
	v.set = append(v.set, e)
	switch {
	case v.idxOn:
		v.setIdx[e.name] = len(v.set) - 1
	case len(v.set) > linearSet:
		if v.setIdx == nil {
			v.setIdx = make(map[string]int, 2*len(v.set))
		}
		for i := range v.set {
			v.setIdx[v.set[i].name] = i
		}
		v.idxOn = true
	}
}

// find returns the position of the answer called name in the set, or -1.
func (v *visitor) find(name string) int {
	if v.idxOn {
		if i, ok := v.setIdx[name]; ok {
			return i
		}
		return -1
	}
	for i := range v.set {
		if v.set[i].name == name {
			return i
		}
	}
	return -1
}

// commitCard ends a model card. The first card that fails, and in it the
// first member in schema order that is of the wrong kind or missing, is the
// failure the list reports; the card keeps its index either way.
func (v *visitor) commitCard() {
	if !v.cardsErr.set && v.cardHas != cardAll {
		for _, m := range cardSchema {
			if v.cardBad&m.bit != 0 {
				v.cardsErr = cardPend(len(v.cards), m.name, errNotString)
				break
			}
			if v.cardHas&m.bit == 0 {
				v.cardsErr = cardPend(len(v.cards), m.name, errMissing)
				break
			}
		}
	}
	v.cards = append(v.cards, v.card)
}
