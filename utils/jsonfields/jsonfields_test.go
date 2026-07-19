package jsonfields_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/miroslav-matejovsky/opdl/utils/jsonfields"
	"github.com/stretchr/testify/require"
)

type InnerA struct {
	Promoted    string `json:"promoted"`
	Conflicting int    `json:"conflict"`
}

type InnerB struct {
	Conflicting int `json:"conflict"`
}

type SampleStruct struct {
	Tagged      string `json:"tagged_name"`
	Untagged    int
	Skipped     bool   `json:"-"`
	OmitEmpty   string `json:"omit,omitempty"`
	WithOption  int    `json:"with_string,string"`
	unexported  string
	EmptyTag    string `json:""`
	Pointer     *int   `json:"ptr"`
	InnerA
	InnerB
}

func TestFields_BasicAndPromotedRules(t *testing.T) {
	t.Parallel()

	fields, err := jsonfields.Fields(reflect.TypeOf(SampleStruct{}))
	require.NoError(t, err)

	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}

	// Declaration order: Tagged, Untagged, OmitEmpty, WithOption, EmptyTag, Pointer, then Promoted from InnerA.
	// Skipped, unexported, and conflicting fields ("conflict") should not be present.
	expectedNames := []string{"tagged_name", "Untagged", "omit", "with_string", "EmptyTag", "ptr", "promoted"}
	require.Equal(t, expectedNames, names)

	// Check omitempty flag.
	for _, f := range fields {
		if f.Name == "omit" {
			require.True(t, f.OmitEmpty)
		} else {
			require.False(t, f.OmitEmpty)
		}
	}
}

func TestFields_CompareWithEncodingJSON(t *testing.T) {
	t.Parallel()

	val := SampleStruct{
		Tagged:     "t",
		Untagged:   1,
		OmitEmpty:  "o",
		WithOption: 42,
		EmptyTag:   "e",
		Pointer:    new(int),
		InnerA:     InnerA{Promoted: "p", Conflicting: 1},
		InnerB:     InnerB{Conflicting: 2},
	}

	data, err := json.Marshal(val)
	require.NoError(t, err)

	var encoded map[string]any
	require.NoError(t, json.Unmarshal(data, &encoded))

	fields, err := jsonfields.Fields(reflect.TypeOf(SampleStruct{}))
	require.NoError(t, err)

	discoveredSet := make(map[string]struct{})
	for _, f := range fields {
		discoveredSet[f.Name] = struct{}{}
	}

	encodedSet := make(map[string]struct{})
	for k := range encoded {
		encodedSet[k] = struct{}{}
	}

	require.Equal(t, encodedSet, discoveredSet, "discovered field names must match encoding/json output keys exactly")
}

func TestFields_UnsupportedTypesAndErrors(t *testing.T) {
	t.Parallel()

	_, err := jsonfields.Fields(nil)
	require.Error(t, err)

	_, err = jsonfields.Fields(reflect.TypeOf(123))
	require.Error(t, err)

	type Unsupported struct {
		Bad chan int `json:"bad"`
	}
	_, err = jsonfields.Fields(reflect.TypeOf(Unsupported{}))
	require.Error(t, err)
}
