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

package codec

import (
	"encoding/json"
	"strconv"
	"unicode/utf8"

	"github.com/bytedance/sonic/ast"

	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// slot says what the next value means. A key or an array position sets it;
// the value consumes it.
type slot uint8

const (
	slotNone slot = iota
	slotIgnore
	slotRoot
	slotModel
	slotUsage
	slotAnswers
	slotInputTokens
	slotOutputTokens
	slotAnswer
	slotType
	slotNoul
	slotChoice
	slotConfidence
	slotScore
	slotProbs
	slotLegend
	slotProbValue
	slotLegendValue
	slotModels
	slotCard
	slotCardName
	slotCardDescription
	slotCardReleaseDate
)

// ctr is a container the visitor reads, on its stack. Containers it does not
// read are counted by visitor.ign instead.
type ctr uint8

const (
	ctrRoot ctr = iota
	ctrUsage
	ctrAnswers
	ctrAnswer
	ctrProbs
	ctrLegend
	ctrModels
	ctrCard
)

// maxDepth is the deepest nesting of containers the visitor reads: the root,
// answers, one answer and its probabilities or legend.
const maxDepth = 4

// mode is the kind of body a traversal reads.
type mode uint8

const (
	modeSystemOne mode = iota
	modeModels
)

// The member bits of an answer.
const (
	mType uint16 = 1 << iota
	mNoul
	mChoice
	mConfidence
	mScore
	mProbs
	mLegend
)

// member is one member an answer of a known kind needs.
type member struct {
	bit  uint16
	name string
}

// schema lists, per known kind, the members an answer of that kind needs
// besides "type", in the order the API's schema declares them. pydantic
// validates the fields of a model in that order and the Python SDK reports
// the first error, so the first member in this order that is missing, of the
// wrong kind or holding a bad entry is the one a failure names.
var schema = [...][]member{
	wire.KindNoul:   {{mNoul, "noul"}},
	wire.KindChoice: {{mChoice, "choice"}, {mConfidence, "confidence"}, {mProbs, "probabilities"}},
	wire.KindScore:  {{mScore, "score"}, {mConfidence, "confidence"}, {mLegend, "legend"}, {mProbs, "probabilities"}},
}

// The model card members, in schema order.
const (
	cardName uint8 = 1 << iota
	cardDescription
	cardReleaseDate
	cardAll = cardName | cardDescription | cardReleaseDate
)

// cardSchema lists the model card members in the order the schema declares
// them.
var cardSchema = [...]struct {
	bit  uint8
	name string
}{
	{cardName, "name"},
	{cardDescription, "description"},
	{cardReleaseDate, "release_date"},
}

// pend is a pending validation failure. It holds the parts of the field path
// and a reason, so recording one allocates nothing; the error value is built
// only when the decode fails.
type pend struct {
	set  bool
	path FieldPath
	err  error
}

// answerPend is a failure inside the answer called name.
func answerPend(name, memberName string, err error) pend {
	return pend{set: true, path: FieldPath{Top: "answers", Name: name, HasName: true, Member: memberName}, err: err}
}

// keyPend is a failure at key inside member memberName of the answer called
// name.
func keyPend(name, memberName, key string, err error) pend {
	return pend{set: true, path: FieldPath{Top: "answers", Name: name, HasName: true, Member: memberName, Key: key, HasKey: true}, err: err}
}

// topPend is a failure at the top-level member top, or at memberName inside
// it.
func topPend(top, memberName string, err error) pend {
	return pend{set: true, path: FieldPath{Top: top, Member: memberName}, err: err}
}

// cardPend is a failure at the model card at index, or at memberName inside
// it.
func cardPend(index int, memberName string, err error) pend {
	return pend{set: true, path: FieldPath{Top: "models", Index: index, HasIndex: true, Member: memberName}, err: err}
}

// probPair is one probability as the traversal met it: its key, its value,
// and whether the value was not a number.
type probPair struct {
	key string
	p   float64
	bad bool
}

// legendPair is one legend level as the traversal met it: its key, its text
// or the mark that it is a JSON object or array, and whether it was neither.
type legendPair struct {
	key        string
	text       string
	structured bool
	bad        bool
}

// answerScratch collects one answer object's members until its end.
type answerScratch struct {
	name   string
	typ    string
	noul   float64
	conf   float64
	score  float64
	choice string
	has    uint16 // members present with the right kind (last occurrence)
	wrong  uint16 // members present with the wrong kind (last occurrence)
}

// entry is one answer name in the visitor's answer set: the answer, or the
// failure that its last occurrence would report, or the type of an answer
// of a kind this version does not model.
type entry struct {
	name       string
	typ        string // the unknown type, when ans.Kind is KindUnknown and err is unset
	ans        wire.Answer
	err        pend
	structured int // legend levels waiting for the lazy pass
}

// fold is the scratch record of one folded probability or legend level: the
// wire position of its first occurrence and whether its last value is bad.
type fold struct {
	first int
	bad   bool
}

// visitor is the ast.Visitor of every decode. It skips nothing: sonic
// validates every token it hands over, including those of members the SDK
// does not read, and every string and key is checked here.
type visitor struct {
	body string
	mode mode

	slot    slot
	ign     int  // depth inside a container nobody reads
	ignSlot slot // the slot whose value that container is
	stack   [maxDepth]ctr
	sp      int

	scanned bool // the body's raw-control scan has run
	rawCtl  bool // its result
	scans   int  // number of body scans (0 or 1), for the tests

	// top-level members
	model      string
	hasModel   bool
	modelErr   pend
	usage      wire.Usage
	hasUsage   bool
	usageErr   pend
	inBad      bool // usage.input_tokens of the last occurrence is bad
	outBad     bool // usage.output_tokens of the last occurrence is bad
	answersErr pend

	// answers
	cur    answerScratch
	curKey string
	set    []entry
	setIdx map[string]int
	idxOn  bool
	probs  []probPair
	legend []legendPair
	folds  []fold
	strIdx map[string]int
	lvlIdx map[uint32]int

	// models
	cards    []wire.ModelCard
	card     wire.ModelCard
	cardHas  uint8
	cardBad  uint8
	cardsErr pend
	hasCards bool
}

var _ ast.Visitor = (*visitor)(nil)

// reset prepares the visitor for a new body, keeping its scratch capacity.
func (v *visitor) reset(body string, m mode) {
	v.body, v.mode = body, m
	v.slot, v.ign, v.ignSlot, v.sp = slotRoot, 0, slotNone, 0
	v.scanned, v.rawCtl, v.scans = false, false, 0
	v.model, v.hasModel, v.modelErr = "", false, pend{}
	v.usage, v.hasUsage, v.usageErr, v.inBad, v.outBad = wire.Usage{}, false, pend{}, false, false
	v.answersErr = pend{}
	v.cur, v.curKey = answerScratch{}, ""
	v.resetAnswers()
	clear(v.probs)
	v.probs = v.probs[:0]
	clear(v.legend)
	v.legend = v.legend[:0]
	clear(v.cards)
	v.cards = v.cards[:0]
	v.card, v.cardHas, v.cardBad = wire.ModelCard{}, 0, 0
	v.cardsErr, v.hasCards = pend{}, false
	// The label index is keyed by strings of the last body; a pooled
	// visitor must not keep that body alive through them.
	clear(v.strIdx)
}

// release drops the visitor's references into the last body, so a pooled
// visitor does not keep it alive.
func (v *visitor) release() {
	v.reset("", modeSystemOne)
}

func (v *visitor) resetAnswers() {
	clear(v.set)
	v.set = v.set[:0]
	if v.idxOn {
		clear(v.setIdx)
		v.idxOn = false
	}
}

// checkString is the per-string check of every string and key: valid UTF-8
// and no raw control character (ruling R23). [ValidString] decides for a
// string without a byte below 0x20; a valid string holding one came from a
// legal escape unless the body holds a raw one inside a string, which the
// body is asked once (control.go).
func (v *visitor) checkString(s string) error {
	if ValidString(s) {
		return nil
	}
	if !utf8.ValidString(s) {
		return errInvalidUTF8
	}
	if !v.scanned {
		v.scanned = true
		v.scans++
		v.rawCtl = rawControlInString(v.body)
	}
	if v.rawCtl {
		return errRawControl
	}
	return nil
}

func (v *visitor) push(c ctr) error {
	if v.sp == maxDepth {
		return errDepth // unreachable: every readable container is at most maxDepth deep
	}
	v.stack[v.sp] = c
	v.sp++
	return nil
}

func (v *visitor) top() ctr { return v.stack[v.sp-1] }

// begin handles an object (isObj) or an array starting in the current slot.
func (v *visitor) begin(isObj bool) error {
	if v.ign > 0 {
		v.ign++
		return nil
	}
	s := v.slot
	v.slot = slotNone
	switch {
	case s == slotRoot:
		if !isObj {
			return errRootNotObject
		}
		return v.push(ctrRoot)
	case s == slotUsage && isObj:
		v.usage, v.hasUsage, v.usageErr, v.inBad, v.outBad = wire.Usage{}, true, pend{}, false, false
		return v.push(ctrUsage)
	case s == slotAnswers && isObj:
		v.resetAnswers()
		v.answersErr = pend{}
		return v.push(ctrAnswers)
	case s == slotAnswer && isObj:
		v.cur = answerScratch{name: v.curKey}
		return v.push(ctrAnswer)
	case s == slotProbs && isObj:
		v.memberOK(mProbs)
		clear(v.probs)
		v.probs = v.probs[:0]
		return v.push(ctrProbs)
	case s == slotLegend && isObj:
		v.memberOK(mLegend)
		clear(v.legend)
		v.legend = v.legend[:0]
		return v.push(ctrLegend)
	case s == slotModels && !isObj:
		clear(v.cards)
		v.cards, v.hasCards, v.cardsErr = v.cards[:0], true, pend{}
		if err := v.push(ctrModels); err != nil {
			return err
		}
		v.slot = slotCard
		return nil
	case s == slotCard && isObj:
		v.card, v.cardHas, v.cardBad = wire.ModelCard{}, 0, 0
		return v.push(ctrCard)
	}
	// Any other container is a value nobody reads, or one of the wrong kind
	// in a slot: traverse it (sonic checks it, the visitor checks its
	// strings) and decide at its end.
	v.ign, v.ignSlot = 1, s
	return nil
}

// end handles the end of an object or an array.
func (v *visitor) end() error {
	if v.ign > 0 {
		v.ign--
		if v.ign == 0 {
			v.wrongKind(v.ignSlot, true)
			v.nextCard()
		}
		return nil
	}
	v.sp--
	switch v.stack[v.sp] {
	case ctrAnswer:
		v.commitAnswer()
	case ctrCard:
		v.commitCard()
	case ctrModels:
		return nil // the array's end: no slot follows
	}
	v.nextCard()
	return nil
}

// nextCard makes the next value of a models array a card.
func (v *visitor) nextCard() {
	if v.sp > 0 && v.top() == ctrModels {
		v.slot = slotCard
	}
}

// wrongKind records a value of the wrong kind in slot s. container says the
// value was an object or an array; for a legend level that is the structured
// form, which is right.
func (v *visitor) wrongKind(s slot, container bool) {
	switch s {
	case slotModel:
		v.hasModel, v.modelErr = true, topPend("model", "", errNotString)
	case slotUsage:
		v.hasUsage, v.usageErr = true, topPend("usage", "", errNotObject)
	case slotAnswers:
		v.resetAnswers()
		v.answersErr = topPend("answers", "", errNotObject)
	case slotInputTokens:
		v.usage.InputTokens, v.usage.HasInputTokens, v.inBad = 0, false, true
	case slotOutputTokens:
		v.usage.OutputTokens, v.usage.HasOutputTokens, v.outBad = 0, false, true
	case slotAnswer:
		v.put(entry{name: v.curKey, err: answerPend(v.curKey, "type", errNotObject)})
	case slotType:
		v.memberWrong(mType)
	case slotNoul:
		v.memberWrong(mNoul)
	case slotChoice:
		v.memberWrong(mChoice)
	case slotConfidence:
		v.memberWrong(mConfidence)
	case slotScore:
		v.memberWrong(mScore)
	case slotProbs:
		v.memberWrong(mProbs)
	case slotLegend:
		v.memberWrong(mLegend)
	case slotProbValue:
		v.probs = append(v.probs, probPair{key: v.curKey, bad: true})
	case slotLegendValue:
		v.legend = append(v.legend, legendPair{key: v.curKey, structured: container, bad: !container})
	case slotModels:
		clear(v.cards)
		v.cards, v.hasCards, v.cardsErr = v.cards[:0], true, topPend("models", "", errNotArray)
	case slotCard:
		if !v.cardsErr.set {
			v.cardsErr = cardPend(len(v.cards), "", errNotObject)
		}
		// The position still counts: the next card has the next index.
		v.cards = append(v.cards, wire.ModelCard{})
	case slotCardName:
		v.cardBad |= cardName
		v.cardHas &^= cardName
	case slotCardDescription:
		v.cardBad |= cardDescription
		v.cardHas &^= cardDescription
	case slotCardReleaseDate:
		v.cardBad |= cardReleaseDate
		v.cardHas &^= cardReleaseDate
	}
}

func (v *visitor) memberOK(bit uint16) {
	v.cur.has |= bit
	v.cur.wrong &^= bit
}

func (v *visitor) memberWrong(bit uint16) {
	v.cur.wrong |= bit
	v.cur.has &^= bit
}

// scalar handles a string (isStr, s), a number (num) or a literal in the
// current slot.
func (v *visitor) scalar(isStr bool, s string, num json.Number) error {
	if v.ign > 0 {
		return nil
	}
	if v.slot == slotRoot {
		return errRootNotObject
	}
	v.assign(isStr, s, num)
	return nil
}

// assign stores a scalar in the current slot, or records it as a value of
// the wrong kind. null is a value of the wrong kind everywhere except in a
// token count, where the Python SDK's Usage takes it as an absent count.
func (v *visitor) assign(isStr bool, s string, num json.Number) {
	sl := v.slot
	v.slot = slotNone
	v.nextCard()
	switch sl {
	case slotNone, slotIgnore:
		return
	case slotModel:
		if isStr {
			v.model, v.hasModel, v.modelErr = s, true, pend{}
			return
		}
	case slotInputTokens, slotOutputTokens:
		n, ok := parseCount(num)
		if !ok {
			break
		}
		if sl == slotInputTokens {
			v.usage.InputTokens, v.usage.HasInputTokens, v.inBad = n, true, false
		} else {
			v.usage.OutputTokens, v.usage.HasOutputTokens, v.outBad = n, true, false
		}
		return
	case slotType:
		if isStr {
			v.cur.typ = s
			v.memberOK(mType)
			return
		}
	case slotChoice:
		if isStr {
			v.cur.choice = s
			v.memberOK(mChoice)
			return
		}
	case slotNoul, slotConfidence, slotScore:
		if f, ok := parseFloat(num); ok {
			switch sl {
			case slotNoul:
				v.cur.noul = f
				v.memberOK(mNoul)
			case slotConfidence:
				v.cur.conf = f
				v.memberOK(mConfidence)
			default:
				v.cur.score = f
				v.memberOK(mScore)
			}
			return
		}
	case slotProbValue:
		f, ok := parseFloat(num)
		v.probs = append(v.probs, probPair{key: v.curKey, p: f, bad: !ok})
		return
	case slotLegendValue:
		v.legend = append(v.legend, legendPair{key: v.curKey, text: s, bad: !isStr})
		return
	case slotCardName, slotCardDescription, slotCardReleaseDate:
		if isStr {
			switch sl {
			case slotCardName:
				v.card.Name = s
				v.cardHas, v.cardBad = v.cardHas|cardName, v.cardBad&^cardName
			case slotCardDescription:
				v.card.Description = s
				v.cardHas, v.cardBad = v.cardHas|cardDescription, v.cardBad&^cardDescription
			default:
				v.card.ReleaseDate = s
				v.cardHas, v.cardBad = v.cardHas|cardReleaseDate, v.cardBad&^cardReleaseDate
			}
			return
		}
	}
	v.wrongKind(sl, false)
}

// parseCount parses a token count: a JSON integer from 0 to 2^64-1. A
// negative or larger integer, a number with a fraction or an exponent, a
// string and a boolean fail (the Python SDK's Usage is strict; a negative
// count and one past uint64 are Appendix B deviations). null never reaches
// it: OnNull takes null as an absent count.
func parseCount(num json.Number) (uint64, bool) {
	if num == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(string(num), 10, 64)
	return n, err == nil
}

// parseFloat parses a JSON number that the traversal delivered as text
// (OnlyNumber). A value out of float64's range (1e400) fails: the Go port
// rejects it where the Python SDK takes infinity (Appendix B).
func parseFloat(num json.Number) (float64, bool) {
	if num == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(string(num), 64)
	return f, err == nil
}

func (v *visitor) OnNull() error {
	if v.ign == 0 && (v.slot == slotInputTokens || v.slot == slotOutputTokens) {
		// null is an absent count.
		if v.slot == slotInputTokens {
			v.usage.InputTokens, v.usage.HasInputTokens, v.inBad = 0, false, false
		} else {
			v.usage.OutputTokens, v.usage.HasOutputTokens, v.outBad = 0, false, false
		}
		v.slot = slotNone
		return nil
	}
	return v.scalar(false, "", "")
}

func (v *visitor) OnBool(bool) error       { return v.scalar(false, "", "") }
func (v *visitor) OnObjectBegin(int) error { return v.begin(true) }
func (v *visitor) OnArrayBegin(int) error  { return v.begin(false) }
func (v *visitor) OnObjectEnd() error      { return v.end() }
func (v *visitor) OnArrayEnd() error       { return v.end() }

func (v *visitor) OnString(s string) error {
	if err := v.checkString(s); err != nil {
		return err
	}
	return v.scalar(true, s, "")
}

func (v *visitor) OnInt64(_ int64, n json.Number) error     { return v.scalar(false, "", n) }
func (v *visitor) OnFloat64(_ float64, n json.Number) error { return v.scalar(false, "", n) }

func (v *visitor) OnObjectKey(key string) error {
	if err := v.checkString(key); err != nil {
		return err
	}
	if v.ign > 0 {
		return nil
	}
	v.curKey = key
	switch v.top() {
	case ctrRoot:
		switch {
		case v.mode == modeModels && key == "models":
			v.slot = slotModels
		case v.mode == modeSystemOne && key == "model":
			v.slot = slotModel
		case v.mode == modeSystemOne && key == "usage":
			v.slot = slotUsage
		case v.mode == modeSystemOne && key == "answers":
			v.slot = slotAnswers
		default:
			v.slot = slotIgnore
		}
	case ctrUsage:
		switch key {
		case "input_tokens":
			v.slot = slotInputTokens
		case "output_tokens":
			v.slot = slotOutputTokens
		default:
			v.slot = slotIgnore
		}
	case ctrAnswers:
		v.slot = slotAnswer
	case ctrAnswer:
		switch key {
		case "type":
			v.slot = slotType
		case "noul":
			v.slot = slotNoul
		case "choice":
			v.slot = slotChoice
		case "confidence":
			v.slot = slotConfidence
		case "score":
			v.slot = slotScore
		case "probabilities":
			v.slot = slotProbs
		case "legend":
			v.slot = slotLegend
		default:
			v.slot = slotIgnore
		}
	case ctrProbs:
		v.slot = slotProbValue
	case ctrLegend:
		v.slot = slotLegendValue
	case ctrCard:
		switch key {
		case "name":
			v.slot = slotCardName
		case "description":
			v.slot = slotCardDescription
		case "release_date":
			v.slot = slotCardReleaseDate
		default:
			v.slot = slotIgnore
		}
	default:
		v.slot = slotIgnore
	}
	return nil
}
