// Package jsonfields describes the fields a Go struct exposes through JSON
// encoding.
//
// It accepts a reflect.Type for a struct and returns ordered field metadata
// exactly matching encoding/json rules for exported fields, empty tag names,
// ignored fields (json:"-"), omitempty options, anonymous embedded fields,
// and conflicting promoted field names.
//
// Fields preserves the declaration order of the struct fields in its returned
// metadata and fails clearly when encountering unsupported or invalid types.
package jsonfields
