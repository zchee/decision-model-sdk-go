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

package typesafe

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/zchee/typesafe-sdk-go/internal/wire"
)

// PreparedFor returns the question set that the struct type T declares: one
// question per answer field, in field order, prepared as [Questions.Prepare]
// prepares a set built by hand.
//
// An answer field is an exported field of type [NoulAnswer], [ChoiceAnswer]
// or [ScoreAnswer] whose struct tag has a "typesafe" key describing the
// question it answers:
//
//	type Ticket struct {
//		Billing typesafe.NoulAnswer   `typesafe:"kind=noul;instructions=Is this about billing, invoices or refunds?;yes=payments or invoices"`
//		Tone    typesafe.ChoiceAnswer `typesafe:"kind=choice;instructions=What is the tone?;options=calm=neutral or polite|angry"`
//		Urgency typesafe.ScoreAnswer  `typesafe:"kind=score;instructions=How urgent?;levels=can wait|this week|today"`
//		Spam    typesafe.NoulAnswer   `typesafe:"kind=noul;optional;instructions=Spam?"`
//	}
//
// PreparedFor[Ticket] asks the same four questions, with the same bytes on
// the wire, as
//
//	typesafe.NewQuestions().
//		Noul("Billing", typesafe.Noul{Instructions: typesafe.Text("Is this about billing, invoices or refunds?"), Yes: typesafe.Text("payments or invoices")}).
//		Choice("Tone", typesafe.Choice{Instructions: typesafe.Text("What is the tone?"), Options: typesafe.Options{{Label: "calm", Description: typesafe.Text("neutral or polite")}, {Label: "angry"}}}).
//		Score("Urgency", typesafe.Score{Instructions: typesafe.Text("How urgent?"), Levels: []typesafe.Content{typesafe.Text("can wait"), typesafe.Text("this week"), typesafe.Text("today")}}).
//		Noul("Spam", typesafe.Noul{Instructions: typesafe.Text("Spam?")}).
//		Prepare()
//
// # Tag grammar
//
// The tag is a list of entries separated by ";". An entry is a key, "=" and
// a value, or the bare key optional. The keys are:
//
//   - kind (required): noul, choice or score; it must agree with the field's
//     type (kind=noul for a NoulAnswer field, and so on).
//   - name: the name the question is asked under and its answer comes back
//     under. Without it the name is the field's name as written, Billing for
//     the field Billing: nothing is lowercased or converted, as encoding/json
//     does with an untagged field. The names type, model, usage and answers
//     are reserved.
//   - instructions: the question's instructions, for every kind.
//   - yes, no: what counts as a yes and as a no answer (kind=noul only), the
//     [Noul] members of the same names.
//   - options: the options of a choice (kind=choice only, and required
//     there), separated by "|", in order. An option is a label, or a label, "=" and its description:
//     options=calm=neutral or polite|angry. Labels must be unique.
//   - levels: the levels of a score (kind=score only, and required there),
//     separated by "|", lowest first. Levels must be unique.
//   - optional: the answer may be absent from a response. It changes
//     nothing on the wire; it tells the typed decoder to leave the field's
//     Present false instead of failing when the answer is missing.
//
// Every value is text, sent as [Text] content; JSON content is written with
// [Questions] instead. A value may contain any character, but four must be
// escaped where they would otherwise end something: "\;" is a ";", "\|" a
// "|", "\=" a "=" and "\\" a backslash. A ";" always ends the entry, so it is
// escaped in every value. A "|" ends an option or a level, so it is escaped
// in every option and level, descriptions included:
// options=calm=neutral|polite|angry asks three options, calm, polite and
// angry, where options=calm=neutral\|polite|angry asks two. The first "=" of
// an option ends its label, and a later "=" belongs to the description. In
// instructions, name, yes and no, "|" and "=" stand for themselves, and in a
// level "=" does; escaping them there is allowed but not needed. No other
// escape exists, keys are never escaped, and no space is trimmed. These are errors: an unknown key, a key given twice, a
// key the kind does not take (yes and no belong to noul, options to choice,
// levels to score), an empty value, an empty entry (";;", or a ";" at either
// end), an empty option, level, label or description, a backslash before
// any other character, a backslash at the end of the tag, and a tag that is
// not valid UTF-8.
//
// A struct tag is itself a Go string literal, so in source each of these
// backslashes is written twice: `typesafe:"kind=noul;instructions=a\\;b"`
// asks "a;b". A single backslash makes the tag malformed (go vet reports it),
// and PreparedFor refuses the field instead of ignoring the tag.
//
// # Rejections
//
// PreparedFor fails with a [*ConfigError] that names T, the field and the
// rule when:
//
//   - two fields ask under the same name;
//   - a choice has no options, or lists an option label twice (a choice built
//     with [Questions] may have none: its empty criteria are sent for the
//     server to judge);
//   - a score lists a level twice;
//   - a score has no levels;
//   - an answer field's tag has no kind, or the field has no tag at all;
//   - the kind is not noul, choice or score;
//   - the kind does not agree with the field's type, or a field that is not
//     an answer field has a typesafe tag;
//   - a field is a pointer to an answer type (write optional instead);
//   - an unexported field has a typesafe tag;
//   - an embedded struct, or pointer to one, has a field with a typesafe tag,
//     directly or in a struct embedded in it: its fields are not promoted;
//   - T is not a struct type (a pointer to a struct included);
//   - a question name is reserved;
//   - optional is given on a field that is not an answer field;
//   - the tag does not follow the grammar above;
//   - T has no answer fields: "At least one question is required.", as
//     [Questions.Prepare] reports for an empty set.
//
// Fields that are neither answer fields nor tagged, and unexported fields
// without a typesafe tag, are ignored. Fields of an embedded struct are not
// promoted: only T's own fields are read, so an embedded struct whose fields
// carry typesafe tags is refused rather than silently skipped, and one
// without such tags is ignored like any other untagged field. A type alias of
// an answer type is that answer type; a type defined from one is not an
// answer type.
//
// # Caching
//
// T is inspected once: the first call builds the set, or the error, and every
// later call for the same T returns that same *Prepared, or that same error,
// for the life of the process, without allocating. The set is read-only and
// safe for concurrent use, like every prepared set; concurrent first calls
// may build it more than once, but all of them return the one that was kept.
func PreparedFor[T any]() (*Prepared, error) {
	p := typedPlanFor[T]()
	return p.prepared, p.err
}

// typedPlan is what [PreparedFor] learns about a struct type: the question
// set it declares, or the error that refuses it, and how each answer field
// maps to a question.
type typedPlan struct {
	// prepared is the question set; nil when err is not.
	prepared *Prepared
	// err is the *ConfigError that refuses the type; nil when it is usable.
	err error
	// fields are the answer fields in field order; fields[i] asks the i-th
	// question of prepared. Nil when err is not.
	fields []typedField
}

// typedField is one answer field of a [typedPlan].
type typedField struct {
	// index is the field's index in its struct, as [reflect.Type.Field] and
	// [reflect.Value.Field] take it.
	index int
	// name is the question name: the answer is looked up under it.
	name string
	// kind is the question's kind, which is also the field's answer type.
	kind wire.Kind
	// optional reports whether the tag has the optional key: the answer may
	// be absent from a response.
	optional bool
	// options are a choice's option labels in order, shared with the
	// prepared set's own table; nil for the other kinds.
	options []string
	// levels are a score's levels, lowest first, shared with the prepared
	// set's own table; nil for the other kinds.
	levels []wire.Content
}

// typedPlans caches the plan of every type PreparedFor has seen: a
// reflect.Type key and a *typedPlan value. It only grows, and holds at most
// one entry per type the program asks about.
var typedPlans sync.Map

// typedPlanFor returns the plan of T, building it on the first call for T.
func typedPlanFor[T any]() *typedPlan { return planFor(reflect.TypeFor[T]()) }

// planFor returns the cached plan of t, building and caching it when there is
// none yet. Failures are cached as well: t cannot change, so building its
// plan again would fail again with the same message.
func planFor(t reflect.Type) *typedPlan {
	if p, ok := typedPlans.Load(t); ok {
		return p.(*typedPlan)
	}
	p, _ := typedPlans.LoadOrStore(t, buildPlan(t))
	return p.(*typedPlan)
}

// The answer types, as reflection sees them.
var (
	noulAnswerType   = reflect.TypeFor[NoulAnswer]()
	choiceAnswerType = reflect.TypeFor[ChoiceAnswer]()
	scoreAnswerType  = reflect.TypeFor[ScoreAnswer]()
)

// answerKind returns the kind of question an answer of type t answers, or
// KindUnknown when t is not an answer type.
func answerKind(t reflect.Type) wire.Kind {
	switch t {
	case noulAnswerType:
		return wire.KindNoul
	case choiceAnswerType:
		return wire.KindChoice
	case scoreAnswerType:
		return wire.KindScore
	default:
		return wire.KindUnknown
	}
}

// answerTypeName is the name of the answer type of questions of kind k.
func answerTypeName(k wire.Kind) string {
	switch k {
	case wire.KindNoul:
		return "NoulAnswer"
	case wire.KindChoice:
		return "ChoiceAnswer"
	default:
		return "ScoreAnswer"
	}
}

// typedQuestion is the question one answer field asks.
type typedQuestion struct {
	entry    questionEntry
	kind     wire.Kind
	optional bool
}

// buildPlan inspects the struct type t and builds its plan.
func buildPlan(t reflect.Type) *typedPlan {
	owner := "PreparedFor[" + typeLabel(t) + "]: "
	if t.Kind() != reflect.Struct {
		msg := owner + typeLabel(t) + " is not a struct type; PreparedFor needs a struct with one answer field per question."
		if t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct {
			msg = owner + typeLabel(t) + " is a pointer; pass the struct type itself: PreparedFor[" + typeLabel(t.Elem()) + "]."
		}
		return &typedPlan{err: newConfigError(msg)}
	}
	var (
		qs     Questions
		fields []typedField
		seen   map[string]int // question name → field index in fields, past repeatScanLimit
	)
	for i := range t.NumField() {
		f := t.Field(i)
		q, asks, err := planField(typeLabel(t), &f)
		if err != nil {
			return &typedPlan{err: err}
		}
		if !asks {
			continue
		}
		if j := earlierField(fields, seen, q.entry.name); j >= 0 {
			return &typedPlan{err: newConfigError(owner + "field " + f.Name + ": Question " + quote(q.entry.name) + " is added more than once; question names must be unique. Field " + t.Field(fields[j].index).Name + " asks it first.")}
		}
		if seen == nil && len(fields) == repeatScanLimit {
			seen = make(map[string]int, 2*repeatScanLimit)
			for j := range fields {
				seen[fields[j].name] = j
			}
		}
		if seen != nil {
			seen[q.entry.name] = len(fields)
		}
		qs.entries = append(qs.entries, q.entry)
		fields = append(fields, typedField{index: i, name: q.entry.name, kind: q.kind, optional: q.optional})
	}
	p, err := qs.Prepare()
	if err != nil {
		// Every rule Prepare applies to a question was checked above, so the
		// one failure left is a set without questions.
		msg := err.Error()
		if len(fields) == 0 {
			msg += " " + typeLabel(t) + " has no answer fields: give it one NoulAnswer, ChoiceAnswer or ScoreAnswer field per question, each with a typesafe tag."
		}
		return &typedPlan{err: newConfigError(owner + msg)}
	}
	entries := p.w.Entries()
	for i := range fields {
		fields[i].options = entries[i].Options
		fields[i].levels = entries[i].Levels
	}
	return &typedPlan{prepared: p, fields: fields}
}

// earlierField returns the index in fields of the field asking under name,
// or -1. seen indexes fields by name once there are more than
// repeatScanLimit of them, and is nil before.
func earlierField(fields []typedField, seen map[string]int, name string) int {
	if seen != nil {
		if j, ok := seen[name]; ok {
			return j
		}
		return -1
	}
	for j := range fields {
		if fields[j].name == name {
			return j
		}
	}
	return -1
}

// typeLabel names t in a message: its qualified name, or "struct {...}" for
// an unnamed struct, whose full spelling would repeat every tag.
func typeLabel(t reflect.Type) string {
	if t.Name() == "" && t.Kind() == reflect.Struct {
		return "struct {...}"
	}
	return t.String()
}

// reservedNames are the question names a typed set cannot use: the members a
// response carries beside its answers, and the discriminator of an answer.
var reservedNames = [...]string{"type", "model", "usage", "answers"}

// planField reads the struct field f of the struct type that outer names,
// and returns the question it asks. asks is false for a field that asks none
// and is ignored.
func planField(outer string, f *reflect.StructField) (q typedQuestion, asks bool, err error) {
	at := "PreparedFor[" + outer + "]: field " + f.Name + ": "
	tag, tagged, malformed := lookupTag(f.Tag)
	if !tagged && !malformed && f.Anonymous {
		if path := promotedTag(f.Name, f.Type, nil); path != "" {
			name := path[strings.LastIndexByte(path, '.')+1:]
			return q, false, newConfigError(at + "fields of an embedded struct are not promoted, and " + path + " has a typesafe tag; declare " + name + " in " + outer + " itself, since PreparedFor reads only the struct's own fields.")
		}
	}
	if !f.IsExported() {
		if tagged || malformed {
			return q, false, newConfigError(at + "the field is unexported and has a typesafe tag; only exported fields are answered: export the field or remove the tag.")
		}
		return q, false, nil
	}
	kind := answerKind(f.Type)
	pointer := f.Type.Kind() == reflect.Pointer && answerKind(derefAll(f.Type)) != wire.KindUnknown
	if malformed {
		return q, false, newConfigError(at + `the typesafe tag is not a valid Go string literal; write each backslash of an escape twice in the struct tag, as in typesafe:"instructions=a\\;b".`)
	}
	if !tagged {
		switch {
		case kind != wire.KindUnknown:
			return q, false, newConfigError(at + "the " + answerTypeName(kind) + " field has no typesafe tag, so no kind; every answer field asks a question: add a tag such as typesafe:\"kind=" + kind.String() + "\".")
		case pointer:
			return q, false, pointerField(at, f.Type)
		default:
			return q, false, nil
		}
	}
	spec, err := parseTag(tag)
	if err != nil {
		return q, false, newConfigError(at + "typesafe tag: " + err.Error() + ".")
	}
	switch {
	case pointer:
		return q, false, pointerField(at, f.Type)
	case kind == wire.KindUnknown && spec.optional:
		return q, false, newConfigError(at + "optional applies only to NoulAnswer, ChoiceAnswer and ScoreAnswer fields, and the field is a " + f.Type.String() + ".")
	case kind == wire.KindUnknown:
		return q, false, newConfigError(at + "a typesafe tag needs a NoulAnswer, ChoiceAnswer or ScoreAnswer field, and the field is a " + f.Type.String() + ".")
	case spec.kind == "":
		return q, false, newConfigError(at + "the typesafe tag has no kind; add kind=" + kind.String() + ".")
	}
	tagKind := wire.ParseKind(spec.kind)
	if tagKind == wire.KindUnknown {
		return q, false, newConfigError(at + "unknown kind " + quote(spec.kind) + "; the kinds are noul, choice and score.")
	}
	if tagKind != kind {
		return q, false, newConfigError(at + "kind=" + spec.kind + " needs a " + answerTypeName(tagKind) + " field, and the field is a " + answerTypeName(kind) + "; make the kind and the field's type agree.")
	}
	if key := spec.keyOutside(kind); key != "" {
		return q, false, newConfigError(at + "typesafe tag: " + key + " does not apply to kind=" + spec.kind + "; " + kindKeys(kind) + ".")
	}
	name := f.Name
	if spec.name != "" {
		name = spec.name
	}
	if slices.Contains(reservedNames[:], name) {
		return q, false, newConfigError(at + "the question name " + quote(name) + " is reserved (type, model, usage and answers are); give the field another name with name=.")
	}
	q = typedQuestion{kind: kind, optional: spec.optional, entry: questionEntry{name: name}}
	switch kind {
	case wire.KindNoul:
		q.entry.form = formNoul
		q.entry.noul = Noul{Instructions: optionalText(spec.instructions), Yes: optionalText(spec.yes), No: optionalText(spec.no)}
	case wire.KindChoice:
		if len(spec.options) == 0 {
			return q, false, newConfigError(at + `Choice question ` + quote(name) + ` has no options; list them, each optionally described, as options=calm=polite|angry.`)
		}
		labels := make([]string, len(spec.options))
		opts := make(Options, len(spec.options))
		for i, o := range spec.options {
			labels[i] = o.label
			opts[i] = Option{Label: o.label, Description: optionalText(o.description)}
		}
		if i := firstRepeat(labels); i >= 0 {
			return q, false, newConfigError(at + `Choice question ` + quote(name) + ` has option ` + quote(labels[i]) + ` more than once; option labels must be unique.`)
		}
		q.entry.form = formChoice
		q.entry.choice = Choice{Instructions: optionalText(spec.instructions), Options: opts}
	case wire.KindScore:
		if len(spec.levels) == 0 {
			return q, false, newConfigError(at + noCriteria(name).Error() + " List the levels, lowest first, as levels=low|high.")
		}
		if i := firstRepeat(spec.levels); i >= 0 {
			return q, false, newConfigError(at + `Score question ` + quote(name) + ` has level ` + quote(spec.levels[i]) + ` more than once; levels must be unique.`)
		}
		levels := make([]Content, len(spec.levels))
		for i, l := range spec.levels {
			levels[i] = Text(l)
		}
		q.entry.form = formScore
		q.entry.score = Score{Instructions: optionalText(spec.instructions), Levels: levels}
	}
	return q, true, nil
}

// promotedTag returns the path, starting at path, of the first field with a
// typesafe key that embedding a field of type t would promote: a field of the
// struct t (or points to), or of a struct embedded in it at any depth. It
// returns "" when there is none, or when t is not a struct or a pointer to
// one. seen holds the struct types on the path so far, so that a struct
// that embeds a pointer to itself ends the walk.
func promotedTag(path string, t reflect.Type, seen []reflect.Type) string {
	t = derefAll(t)
	if t.Kind() != reflect.Struct || slices.Contains(seen, t) {
		return ""
	}
	seen = append(seen, t)
	for f := range t.Fields() {
		if _, tagged, malformed := lookupTag(f.Tag); tagged || malformed {
			return path + "." + f.Name
		}
		if f.Anonymous {
			if found := promotedTag(path+"."+f.Name, f.Type, seen); found != "" {
				return found
			}
		}
	}
	return ""
}

// pointerField is the refusal of a field whose type is a pointer to an answer
// type.
func pointerField(at string, t reflect.Type) *ConfigError {
	elem := derefAll(t)
	return newConfigError(at + "the field is a pointer, " + t.String() + "; answer fields are values: make it a " + answerTypeName(answerKind(elem)) + ", with optional in its tag if the answer may be absent.")
}

// maxDeref is how many pointers derefAll follows. A pointer type can point
// to itself (type P *P), so the walk needs a bound; no field is a pointer to
// a pointer this many times over.
const maxDeref = 8

// derefAll returns the type t points to through up to maxDeref pointers, or
// the pointer type reached after that many.
func derefAll(t reflect.Type) reflect.Type {
	for range maxDeref {
		if t.Kind() != reflect.Pointer {
			break
		}
		t = t.Elem()
	}
	return t
}

// optionalText is s as text content, or unset content when s is empty: the
// grammar has no empty values, so an empty string is a key that was not
// given.
func optionalText(s string) Content {
	if s == "" {
		return Content{}
	}
	return Text(s)
}

// kindKeys lists the keys a question of kind k takes, for a message.
func kindKeys(k wire.Kind) string {
	switch k {
	case wire.KindNoul:
		return "a noul takes kind, name, instructions, yes, no and optional"
	case wire.KindChoice:
		return "a choice takes kind, name, instructions, options and optional"
	default:
		return "a score takes kind, name, instructions, levels and optional"
	}
}

// lookupTag returns the value of the "typesafe" key of the struct tag tag,
// as [reflect.StructTag.Lookup] does, and whether the key is there. Unlike
// Lookup it also reports malformed when the key is there but its value is
// not a valid Go string literal, such as a value with a single backslash
// before ";": Lookup would report such a tag as absent, and the field would
// be taken for an untagged one.
func lookupTag(tag reflect.StructTag) (value string, ok, malformed bool) {
	// The loop is reflect.StructTag.Lookup's, which follows the
	// conventional key:"value" format.
	for tag != "" {
		i := 0
		for i < len(tag) && tag[i] == ' ' {
			i++
		}
		tag = tag[i:]
		if tag == "" {
			break
		}
		i = 0
		for i < len(tag) && tag[i] > ' ' && tag[i] != ':' && tag[i] != '"' && tag[i] != 0x7f {
			i++
		}
		if i == 0 || i+1 >= len(tag) || tag[i] != ':' || tag[i+1] != '"' {
			break
		}
		name := string(tag[:i])
		tag = tag[i+1:]
		i = 1
		for i < len(tag) && tag[i] != '"' {
			if tag[i] == '\\' {
				i++
			}
			i++
		}
		if i >= len(tag) {
			return "", false, name == "typesafe"
		}
		qvalue := string(tag[:i+1])
		tag = tag[i+1:]
		if name == "typesafe" {
			v, err := strconv.Unquote(qvalue)
			if err != nil {
				return "", false, true
			}
			return v, true, false
		}
	}
	return "", false, false
}

// tagKey is one key of the tag grammar, as a bit of a key set.
type tagKey uint8

const (
	keyKind tagKey = 1 << iota
	keyName
	keyInstructions
	keyYes
	keyNo
	keyOptions
	keyLevels
	keyOptional
)

// tagKeys maps each key's spelling to its bit.
var tagKeys = map[string]tagKey{
	"kind":         keyKind,
	"name":         keyName,
	"instructions": keyInstructions,
	"yes":          keyYes,
	"no":           keyNo,
	"options":      keyOptions,
	"levels":       keyLevels,
	"optional":     keyOptional,
}

// tagSpec is a parsed typesafe tag. A value is never empty when its key was
// given, so the empty string means a key that was not.
type tagSpec struct {
	kind         string
	name         string
	instructions string
	yes          string
	no           string
	options      []tagOption
	levels       []string
	optional     bool
	// keys are the keys the tag gives.
	keys tagKey
}

// tagOption is one option of an options value.
type tagOption struct {
	label string
	// description is empty when the option has none.
	description string
}

// keyOutside returns the first key of s, in the grammar's order, that a
// question of kind k does not take, or "" when every key of s applies.
func (s *tagSpec) keyOutside(k wire.Kind) string {
	var outside tagKey
	switch k {
	case wire.KindNoul:
		outside = keyOptions | keyLevels
	case wire.KindChoice:
		outside = keyYes | keyNo | keyLevels
	default:
		outside = keyYes | keyNo | keyOptions
	}
	switch bad := s.keys & outside; {
	case bad == 0:
		return ""
	case bad&keyYes != 0:
		return "yes"
	case bad&keyNo != 0:
		return "no"
	case bad&keyOptions != 0:
		return "options"
	default:
		return "levels"
	}
}

// tagError is a tag that does not follow the grammar. [PreparedFor] reports
// it as a [*ConfigError] naming the field.
type tagError struct{ msg string }

func (e *tagError) Error() string { return e.msg }

// parseTag parses the value of a typesafe struct tag. It is a pure function
// of tag: it checks the grammar only, and leaves the rules that depend on the
// field (the kind, its keys, repeated options and levels) to the caller. An
// empty tag has no entries and parses to the zero tagSpec.
func parseTag(tag string) (tagSpec, error) {
	var s tagSpec
	if !utf8.ValidString(tag) {
		return s, &tagError{"the tag is not valid UTF-8"}
	}
	for rest := tag; rest != ""; {
		end, err := scanEntry(rest, ';')
		if err != nil {
			return tagSpec{}, err
		}
		if err := s.addEntry(rest[:end]); err != nil {
			return tagSpec{}, err
		}
		if end == len(rest) {
			break
		}
		rest = rest[end+1:]
		if rest == "" {
			return tagSpec{}, &tagError{`empty entry: the tag ends with ";"`}
		}
	}
	return s, nil
}

// errUnterminated is a backslash at the end of the tag, which escapes
// nothing.
var errUnterminated = &tagError{`unterminated escape: a "\" at the end of the tag; write "\\" for a backslash`}

// scanEntry returns the index in s of the first sep that no backslash
// escapes, or len(s) when there is none. A backslash at the end of s is an
// unterminated escape.
func scanEntry(s string, sep byte) (int, error) {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 == len(s) {
				return 0, errUnterminated
			}
			i++
		case sep:
			return i, nil
		}
	}
	return len(s), nil
}

// addEntry parses the entry e, a key and its value, into s.
func (s *tagSpec) addEntry(e string) error {
	if e == "" {
		return &tagError{`empty entry: two ";" in a row, or a ";" at the start of the tag`}
	}
	key, value, hasValue := strings.Cut(e, "=")
	bit, known := tagKeys[key]
	if !known {
		return &tagError{"unknown key " + quote(key) + "; the keys are kind, name, instructions, yes, no, options, levels and optional"}
	}
	if s.keys&bit != 0 {
		return &tagError{"the key " + key + " is given twice"}
	}
	s.keys |= bit
	if bit == keyOptional {
		if hasValue {
			return &tagError{"optional takes no value: write it bare, as optional"}
		}
		s.optional = true
		return nil
	}
	if value == "" {
		return &tagError{"the key " + key + " needs a nonempty value, as in " + key + "=..."}
	}
	var err error
	switch bit {
	case keyKind:
		s.kind, err = unescape(value)
	case keyName:
		s.name, err = unescape(value)
	case keyInstructions:
		s.instructions, err = unescape(value)
	case keyYes:
		s.yes, err = unescape(value)
	case keyNo:
		s.no, err = unescape(value)
	case keyOptions:
		s.options, err = parseOptions(value)
	case keyLevels:
		s.levels, err = parseLevels(value)
	}
	return err
}

// parseOptions parses an options value: options separated by "|", each a
// label or a label, "=" and a description.
func parseOptions(value string) ([]tagOption, error) {
	var opts []tagOption
	for rest := value; ; {
		// The entry scan found every escape terminated, so these scans
		// cannot fail.
		end, _ := scanEntry(rest, '|')
		item := rest[:end]
		if item == "" {
			return nil, &tagError{"empty option in options: two \"|\" in a row, or a \"|\" at either end"}
		}
		sep, _ := scanEntry(item, '=')
		label, err := unescape(item[:sep])
		if err != nil {
			return nil, err
		}
		if label == "" {
			return nil, &tagError{"an option of options has an empty label"}
		}
		var description string
		if sep < len(item) {
			if description, err = unescape(item[sep+1:]); err != nil {
				return nil, err
			}
			if description == "" {
				return nil, &tagError{"option " + quote(label) + ` has an empty description; leave out the "=" for an option without one`}
			}
		}
		opts = append(opts, tagOption{label: label, description: description})
		if end == len(rest) {
			return opts, nil
		}
		rest = rest[end+1:]
	}
}

// parseLevels parses a levels value: levels separated by "|".
func parseLevels(value string) ([]string, error) {
	var levels []string
	for rest := value; ; {
		end, _ := scanEntry(rest, '|') // cannot fail, as in parseOptions
		if end == 0 {
			return nil, &tagError{"empty level in levels: two \"|\" in a row, or a \"|\" at either end"}
		}
		level, err := unescape(rest[:end])
		if err != nil {
			return nil, err
		}
		levels = append(levels, level)
		if end == len(rest) {
			return levels, nil
		}
		rest = rest[end+1:]
	}
}

// unescape replaces the escapes of s by the characters they stand for. It
// returns s itself when s has none.
func unescape(s string) (string, error) {
	i := strings.IndexByte(s, '\\')
	if i < 0 {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s) - 1)
	b.WriteString(s[:i])
	for ; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		// parseTag's entry scan refuses a backslash at the end of the tag,
		// so every backslash here has a byte after it; the check keeps a
		// direct caller from indexing past the end.
		if i++; i == len(s) {
			return "", errUnterminated
		}
		switch c = s[i]; c {
		case ';', '|', '=', '\\':
			b.WriteByte(c)
		default:
			r, _ := utf8.DecodeRuneInString(s[i:])
			return "", &tagError{`unknown escape: "\" before ` + strconv.QuoteRune(r) + `; the escapes are \;, \|, \= and \\`}
		}
	}
	return b.String(), nil
}
