package catalog

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// DataType is the value type of an attribute.
type DataType string

// Attribute data types (attributes.data_type).
const (
	DataText        DataType = "text"
	DataNumber      DataType = "number"
	DataBoolean     DataType = "boolean"
	DataDate        DataType = "date"
	DataSelect      DataType = "select"
	DataMultiselect DataType = "multiselect"
)

// Limits of attribute values.
const (
	// MaxAttributesBytes caps the JSON object of a product's attribute values.
	MaxAttributesBytes = 16 << 10
	// MaxTextValueLen caps a text value, in characters.
	MaxTextValueLen = 500
	// MaxMultiselectValues caps the options chosen for one multiselect value.
	MaxMultiselectValues = 50
	// numberScale and numberMaxAbs bound number values like a numeric(18,6) column.
	numberScale = 6
)

var numberMaxAbs = decimal.New(1, 12)

// HasOptions reports whether values of the type are attribute option ids.
func (t DataType) HasOptions() bool { return t == DataSelect || t == DataMultiselect }

// Valid reports whether t is a known data type.
func (t DataType) Valid() bool {
	switch t {
	case DataText, DataNumber, DataBoolean, DataDate, DataSelect, DataMultiselect:
		return true
	}
	return false
}

// ValueValidator checks one attribute value. raw is the JSON value the client sent (never JSON
// null: null clears the value before validators run); previous is the stored value or nil. It
// returns the normalised value to store, or a reason code.
type ValueValidator interface {
	Validate(attr Attribute, raw, previous json.RawMessage) (normalised any, reason string)
}

// ValueValidatorFunc adapts a function to ValueValidator.
type ValueValidatorFunc func(attr Attribute, raw, previous json.RawMessage) (any, string)

// Validate implements ValueValidator.
func (f ValueValidatorFunc) Validate(attr Attribute, raw, previous json.RawMessage) (any, string) {
	return f(attr, raw, previous)
}

// AttributeTypeRegistry maps every data type to its value validator.
type AttributeTypeRegistry map[DataType]ValueValidator

// DefaultAttributeTypes returns the validators of the built-in data types.
func DefaultAttributeTypes() AttributeTypeRegistry {
	return AttributeTypeRegistry{
		DataText:        ValueValidatorFunc(validateText),
		DataNumber:      ValueValidatorFunc(validateNumber),
		DataBoolean:     ValueValidatorFunc(validateBoolean),
		DataDate:        ValueValidatorFunc(validateDate),
		DataSelect:      ValueValidatorFunc(validateSelect),
		DataMultiselect: ValueValidatorFunc(validateMultiselect),
	}
}

func validateText(_ Attribute, raw, _ json.RawMessage) (any, string) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, "invalid_type"
	}
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) || strings.ContainsFunc(s, isControl) {
		return nil, "invalid_text"
	}
	if utf8.RuneCountInString(s) > MaxTextValueLen {
		return nil, "too_long"
	}
	return s, ""
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

func validateNumber(_ Attribute, raw, _ json.RawMessage) (any, string) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || (trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9')) {
		return nil, "invalid_type"
	}
	var n json.Number
	if err := json.Unmarshal(trimmed, &n); err != nil {
		return nil, "invalid_type"
	}
	d, err := decimal.NewFromString(n.String())
	if err != nil {
		return nil, "invalid_type"
	}
	if d.Abs().Cmp(numberMaxAbs) >= 0 {
		return nil, "out_of_range"
	}
	if !d.Equal(d.Round(numberScale)) {
		return nil, "too_many_decimals"
	}
	return json.Number(d.String()), ""
}

func validateBoolean(_ Attribute, raw, _ json.RawMessage) (any, string) {
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, "invalid_type"
	}
	return b, ""
}

func validateDate(_ Attribute, raw, _ json.RawMessage) (any, string) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, "invalid_type"
	}
	if _, err := time.Parse(time.DateOnly, s); err != nil {
		return nil, "invalid_date"
	}
	return s, ""
}

// optionReason checks that id is an option of attr; an inactive option is only accepted when the
// stored value already holds it.
func optionReason(attr Attribute, id uuid.UUID, kept func(uuid.UUID) bool) string {
	for _, o := range attr.Options {
		if o.ID == id {
			if !o.Active && !kept(id) {
				return "option_inactive"
			}
			return ""
		}
	}
	return "unknown_option"
}

func validateSelect(attr Attribute, raw, previous json.RawMessage) (any, string) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, "invalid_type"
	}
	id, err := ids.Parse(s)
	if err != nil {
		return nil, "unknown_option"
	}
	var prev string
	_ = json.Unmarshal(previous, &prev) // a previous value of another shape simply does not match
	if reason := optionReason(attr, id, func(o uuid.UUID) bool { return prev == o.String() }); reason != "" {
		return nil, reason
	}
	return id.String(), ""
}

func validateMultiselect(attr Attribute, raw, previous json.RawMessage) (any, string) {
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, "invalid_type"
	}
	if len(values) > MaxMultiselectValues {
		return nil, "too_many_options"
	}
	var prev []string
	_ = json.Unmarshal(previous, &prev) // a previous value of another shape keeps nothing
	out := make([]string, 0, len(values))
	for _, v := range values {
		id, err := ids.Parse(v)
		if err != nil {
			return nil, "unknown_option"
		}
		if slices.Contains(out, id.String()) {
			return nil, "duplicate_option"
		}
		if reason := optionReason(attr, id, func(o uuid.UUID) bool { return slices.Contains(prev, o.String()) }); reason != "" {
			return nil, reason
		}
		out = append(out, id.String())
	}
	slices.Sort(out)
	return out, ""
}

// SetAttribute is an attribute of a product's attribute set, as validation sees it.
type SetAttribute struct {
	Attribute Attribute
	Required  bool
}

// AttributeValidator validates the attribute values of a product against its attribute set.
type AttributeValidator struct {
	Types AttributeTypeRegistry
}

// NewAttributeValidator returns a validator using the built-in data types.
func NewAttributeValidator() AttributeValidator {
	return AttributeValidator{Types: DefaultAttributeTypes()}
}

// Validate returns the attribute object to store. input holds the values of the set's attributes
// (a missing key or JSON null clears the value). Values stored for attributes that are no longer
// in the set are kept untouched (removing an attribute from a set hides its values, never deletes
// them); input may echo such a value back unchanged but cannot change it. An inactive attribute
// keeps its stored value but takes no new one. Field errors are keyed "attributes.<code>".
func (v AttributeValidator) Validate(input []byte, set []SetAttribute, previous JSONObject) (JSONObject, error) {
	if len(input) > MaxAttributesBytes {
		return nil, apperr.NewValidation("attributes_too_large", "The attribute values are too large",
			map[string]string{"attributes": "too_large"})
	}
	values, err := decodeObject(input)
	if err != nil {
		return nil, errInvalid(map[string]string{"attributes": "not_an_object"})
	}
	prev, err := decodeObject(previous)
	if err != nil {
		prev = map[string]json.RawMessage{}
	}

	out := map[string]any{}
	fields := map[string]string{}
	inSet := map[string]bool{}
	for _, sa := range set {
		a := sa.Attribute
		inSet[a.Code] = true
		raw, given := values[a.Code]
		if given && isJSONNull(raw) {
			given = false
		}
		if !given {
			if sa.Required && a.Status == StatusActive {
				fields["attributes."+a.Code] = "required"
			}
			continue
		}
		if a.Status != StatusActive {
			if p, ok := prev[a.Code]; ok && jsonEqual(p, raw) {
				out[a.Code] = p
				continue
			}
			fields["attributes."+a.Code] = "attribute_inactive"
			continue
		}
		validator, ok := v.Types[a.DataType]
		if !ok {
			fields["attributes."+a.Code] = "unsupported_type"
			continue
		}
		norm, reason := validator.Validate(a, raw, prev[a.Code])
		if reason != "" {
			fields["attributes."+a.Code] = reason
			continue
		}
		if s, isText := norm.(string); isText && s == "" && a.DataType == DataText {
			if sa.Required {
				fields["attributes."+a.Code] = "required"
			}
			continue
		}
		out[a.Code] = norm
	}
	for key, raw := range values {
		if inSet[key] {
			continue
		}
		if p, ok := prev[key]; ok && jsonEqual(p, raw) {
			continue // a hidden value echoed back unchanged
		}
		if isJSONNull(raw) {
			if _, ok := prev[key]; !ok {
				continue
			}
		}
		fields["attributes."+key] = "not_in_attribute_set"
	}
	if len(fields) > 0 {
		return nil, errInvalid(fields)
	}
	for key, raw := range prev {
		if !inSet[key] {
			out[key] = raw
		}
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxAttributesBytes {
		return nil, apperr.NewValidation("attributes_too_large", "The attribute values are too large",
			map[string]string{"attributes": "too_large"})
	}
	return JSONObject(encoded), nil
}

// decodeObject decodes a JSON object; empty input is the empty object.
func decodeObject(raw []byte) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m == nil { // the JSON literal null
		return map[string]json.RawMessage{}, nil
	}
	return m, nil
}

func isJSONNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

// jsonEqual compares two JSON values semantically (key order and spacing do not matter).
func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ea, _ := json.Marshal(x) // values decoded from JSON always re-encode
	eb, _ := json.Marshal(y)
	return bytes.Equal(ea, eb)
}

// colorLikeAttribute reports whether an attribute name looks like the fixed colors, which are
// not attributes (they live on lots and document lines).
func colorLikeAttribute(name string, colorNameKeys []string) bool {
	key := nameKey(name)
	if key == "mau" || key == "mau sac" || key == "color" || key == "colour" {
		return true
	}
	return slices.Contains(colorNameKeys, key)
}

// AttributeWarningColor is the warning returned when an attribute duplicates the fixed colors.
const AttributeWarningColor = "color_is_not_an_attribute"
