package jsonfields

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unicode"
)

// Field describes one struct field exposed to JSON encoding.
type Field struct {
	// Name is the encoded JSON property name.
	Name string
	// Type is the Go type of the field.
	Type reflect.Type
	// OmitEmpty is true if the json tag specifies omitempty.
	OmitEmpty bool
}

type candidate struct {
	field  Field
	index  []int
	depth  int
	tagged bool
}

// Fields extracts and describes the JSON-exposed fields for the struct type t
// in declaration order, strictly following encoding/json rules.
func Fields(t reflect.Type) ([]Field, error) {
	if t == nil {
		return nil, errors.New("jsonfields: type is nil")
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("jsonfields: expected struct type, got %s", t.Kind())
	}

	candidates, err := collectCandidates(t)
	if err != nil {
		return nil, err
	}

	resolved := resolveConflicts(candidates)
	slices.SortFunc(resolved, func(a, b candidate) int {
		return slices.Compare(a.index, b.index)
	})

	result := make([]Field, len(resolved))
	for i, r := range resolved {
		result[i] = r.field
	}
	return result, nil
}

type queueItem struct {
	typ   reflect.Type
	index []int
	depth int
}

func collectCandidates(t reflect.Type) ([]candidate, error) {
	var candidates []candidate
	current := []queueItem{}
	next := []queueItem{{typ: t, index: nil, depth: 0}}
	var count, nextCount map[reflect.Type]int
	visited := make(map[reflect.Type]bool)

	for len(next) > 0 {
		current, next = next, current[:0]
		count, nextCount = nextCount, make(map[reflect.Type]int)

		for _, item := range current {
			if visited[item.typ] {
				continue
			}
			visited[item.typ] = true

			for i := 0; i < item.typ.NumField(); i++ {
				f := item.typ.Field(i)
				if shouldIgnore(f) {
					continue
				}
				idx := append(slices.Clone(item.index), i)
				name, options := jsonTag(f)

				if embed, ok := embeddedStruct(f, name); ok {
					nextCount[embed]++
					if nextCount[embed] == 1 {
						next = append(next, queueItem{typ: embed, index: idx, depth: item.depth + 1})
					}
					continue
				}
				if !f.IsExported() {
					continue
				}

				c, err := newCandidate(f, idx, item.depth, name, options)
				if err != nil {
					return nil, err
				}
				candidates = append(candidates, c)
				if count[item.typ] > 1 {
					// Preserve duplicate embedding paths so conflict resolution
					// sees the same ambiguity as encoding/json.
					candidates = append(candidates, c)
				}
			}
		}
	}
	return candidates, nil
}

func shouldIgnore(f reflect.StructField) bool {
	if f.Anonymous {
		t := f.Type
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if !f.IsExported() && t.Kind() != reflect.Struct {
			return true
		}
	} else if !f.IsExported() {
		return true
	}
	return f.Tag.Get("json") == "-"
}

func embeddedStruct(f reflect.StructField, tagName string) (reflect.Type, bool) {
	if !f.Anonymous || tagName != "" {
		return nil, false
	}
	ft := f.Type
	if ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}
	if ft.Kind() == reflect.Struct {
		return ft, true
	}
	return nil, false
}

func newCandidate(f reflect.StructField, index []int, depth int, name string, options []string) (candidate, error) {
	tagged := name != ""
	if !tagged {
		name = f.Name
	}
	omitempty := false
	for _, opt := range options {
		if opt == "omitempty" {
			omitempty = true
		}
	}
	if err := validateType(f.Type); err != nil {
		return candidate{}, fmt.Errorf("jsonfields: field %q has unsupported type: %w", name, err)
	}
	return candidate{
		field:  Field{Name: name, Type: f.Type, OmitEmpty: omitempty},
		index:  index,
		depth:  depth,
		tagged: tagged,
	}, nil
}

func jsonTag(f reflect.StructField) (name string, options []string) {
	parts := strings.Split(f.Tag.Get("json"), ",")
	if !isValidTag(parts[0]) {
		parts[0] = ""
	}
	return parts[0], parts[1:]
}

func isValidTag(name string) bool {
	if name == "" {
		return false
	}
	for _, char := range name {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", char):
		case !unicode.IsLetter(char) && !unicode.IsDigit(char):
			return false
		}
	}
	return true
}

func resolveConflicts(candidates []candidate) []candidate {
	byName := make(map[string][]candidate)
	for _, c := range candidates {
		byName[c.field.Name] = append(byName[c.field.Name], c)
	}

	var resolved []candidate
	for _, group := range byName {
		minDepth := group[0].depth
		for _, c := range group[1:] {
			if c.depth < minDepth {
				minDepth = c.depth
			}
		}
		var atMinDepth []candidate
		for _, c := range group {
			if c.depth == minDepth {
				atMinDepth = append(atMinDepth, c)
			}
		}
		if len(atMinDepth) == 1 {
			resolved = append(resolved, atMinDepth[0])
			continue
		}
		var tagged []candidate
		for _, c := range atMinDepth {
			if c.tagged {
				tagged = append(tagged, c)
			}
		}
		if len(tagged) == 1 {
			resolved = append(resolved, tagged[0])
		}
	}
	return resolved
}

func validateType(t reflect.Type) error {
	if t == nil {
		return nil
	}
	switch t.Kind() {
	case reflect.Chan, reflect.Func, reflect.Complex64, reflect.Complex128, reflect.UnsafePointer:
		return fmt.Errorf("unsupported kind %s", t.Kind())
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return validateType(t.Elem())
	case reflect.Map:
		if err := validateType(t.Key()); err != nil {
			return err
		}
		return validateType(t.Elem())
	case reflect.Struct:
		// Nested structs are inspected by their consumer. Stopping here also
		// keeps recursive structs finite.
		return nil
	default:
		return nil
	}
}
