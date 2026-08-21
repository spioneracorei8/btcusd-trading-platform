package helper

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// # Strategy parameters, declared beside the fields they set
//
// Every parameter a run may vary is declared with a `param` struct tag on the
// field it writes:
//
//	FastPeriod int             `param:"fast,step=1"`
//	Levels     strategy.Levels `param:",inline"`
//	LongOnly   bool            `param:"-"`
//
// # Why tags rather than a table of parameter descriptions
//
// A hand-written table drifts. Someone adds a field, forgets the table, and
// the parameter is unreachable — which looks exactly like a parameter that was
// deliberately withheld, and nothing in a report distinguishes them. Declaring
// the name next to the field it sets makes the two impossible to separate, and
// DescribeParams *refuses* a struct with an untagged exported field, so the
// omission is a build-time failure of the test that walks every config rather
// than a silent gap.
//
// # What "-" means, and why some fields must have it
//
// Not every field of a configuration is a parameter. RoundTripCostPct and
// LongOnly are derived from the cost model and the market type, and a run that
// could set them could be configured to contradict the venue it claims to
// model — a spot run taking shorts, or a strategy validated against a fee it
// does not pay. They are marked "-" so that is unexpressible rather than
// merely discouraged.

// ParamKind is the type of a parameter, as reported to a human.
type ParamKind string

// The parameter kinds.
const (
	ParamInt    ParamKind = "int"
	ParamFloat  ParamKind = "float"
	ParamBool   ParamKind = "bool"
	ParamString ParamKind = "string"
	ParamList   ParamKind = "list"
)

// ParamSpec describes one settable parameter.
type ParamSpec struct {
	Name    string
	Kind    ParamKind
	Default string

	// Step is how far one neighbour sits from the chosen value, declared in
	// the parameter's own units. It is empty when the parameter has no
	// meaningful neighbour.
	//
	// # Why the step is declared and not computed
	//
	// A blanket rule — ten percent, say — asks a different question at
	// different magnitudes: ten percent of an EMA period of 200 is twenty
	// bars, and of a 0.5 ATR multiple is 0.05. Neither is what a human means
	// by "one step either side". Declaring it beside the field is the only
	// place that knows what a step of *this* parameter is.
	Step string

	path []int
}

// ParamChange is one parameter that differs from its default.
type ParamChange struct {
	Name string
	From string
	To   string
}

// DescribeParams lists the settable parameters of a configuration struct.
//
// config may be a struct or a pointer to one. The order is declaration order,
// which reads better than alphabetical: a strategy's parameters are usually
// written in the order someone would think about them.
func DescribeParams(config any) ([]ParamSpec, error) {
	value, err := paramStruct(config)
	if err != nil {
		return nil, err
	}

	var specs []ParamSpec
	if err := walkParams(value, nil, &specs); err != nil {
		return nil, err
	}
	return specs, nil
}

// ParamNames lists the settable parameter names, sorted, for an error message.
func ParamNames(config any) []string {
	specs, err := DescribeParams(config)
	if err != nil {
		return nil
	}

	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	sort.Strings(names)
	return names
}

// ApplyParams writes overrides onto config, which must be a pointer.
//
// An unknown key is an error naming every valid key. A silently ignored typo
// means running the default while believing otherwise, and no report produced
// afterwards could show that it happened.
//
// Values are only parsed here. Whether a parsed value is *allowed* is left
// entirely to the configuration's own Validate, so there is one set of rules
// rather than two that can disagree.
func ApplyParams(config any, overrides map[string]string) error {
	if len(overrides) == 0 {
		return nil
	}

	pointer := reflect.ValueOf(config)
	if pointer.Kind() != reflect.Pointer || pointer.IsNil() {
		return fmt.Errorf("params: need a non-nil pointer to a struct, got %T", config)
	}

	specs, err := DescribeParams(config)
	if err != nil {
		return err
	}
	byName := make(map[string]ParamSpec, len(specs))
	for _, spec := range specs {
		byName[spec.Name] = spec
	}

	// Sorted, so a configuration with two bad keys reports the same one first
	// every time. Map iteration order would make the message vary between
	// otherwise identical runs.
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		spec, ok := byName[key]
		if !ok {
			return fmt.Errorf("unknown parameter %q; this strategy takes %s",
				key, strings.Join(ParamNames(config), ", "))
		}
		if err := setParam(pointer.Elem(), spec, overrides[key]); err != nil {
			return err
		}
	}
	return nil
}

// StepParam moves one parameter by its declared step, in the given direction.
//
// config must be a pointer to a copy: this mutates it. A parameter with no
// declared step is an error rather than a silent no-op, because a neighbourhood
// table missing a row reads as a neighbour that was tested and behaved the
// same.
func StepParam(config any, name string, direction int) error {
	pointer := reflect.ValueOf(config)
	if pointer.Kind() != reflect.Pointer || pointer.IsNil() {
		return fmt.Errorf("params: need a non-nil pointer to a struct, got %T", config)
	}

	specs, err := DescribeParams(config)
	if err != nil {
		return err
	}

	for _, spec := range specs {
		if spec.Name != name {
			continue
		}
		if spec.Step == "" {
			return fmt.Errorf("parameter %q declares no neighbourhood step, so it cannot be varied one step either side", name)
		}
		return stepValue(fieldByPath(pointer.Elem(), spec.path), spec, direction)
	}
	return fmt.Errorf("unknown parameter %q", name)
}

// ChangedParams reports the parameters of configured that differ from defaults.
//
// Both must be the same type. It compares the *values* rather than tracking
// what was set, so a parameter passed at its default value is correctly
// reported as unchanged — the header answers "what is different about this
// run", not "what did somebody type".
func ChangedParams(defaults, configured any) ([]ParamChange, error) {
	before, err := paramStruct(defaults)
	if err != nil {
		return nil, err
	}
	after, err := paramStruct(configured)
	if err != nil {
		return nil, err
	}
	if before.Type() != after.Type() {
		return nil, fmt.Errorf("params: cannot compare %s with %s", before.Type(), after.Type())
	}

	specs, err := DescribeParams(defaults)
	if err != nil {
		return nil, err
	}

	var changes []ParamChange
	for _, spec := range specs {
		was := formatParam(fieldByPath(before, spec.path))
		is := formatParam(fieldByPath(after, spec.path))
		if was != is {
			changes = append(changes, ParamChange{Name: spec.Name, From: was, To: is})
		}
	}
	return changes, nil
}

// paramStruct resolves a struct or pointer-to-struct to the struct value.
func paramStruct(config any) (reflect.Value, error) {
	value := reflect.ValueOf(config)
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return reflect.Value{}, fmt.Errorf("params: %T is nil", config)
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("params: need a struct, got %T", config)
	}
	return value, nil
}

// walkParams collects the specs of a struct, descending into inlined fields.
func walkParams(value reflect.Value, path []int, specs *[]ParamSpec) error {
	structType := value.Type()

	for i := range structType.NumField() {
		field := structType.Field(i)
		if !field.IsExported() {
			continue
		}

		tag, tagged := field.Tag.Lookup("param")
		if !tagged {
			return fmt.Errorf(
				"params: %s.%s has no `param` tag. Every exported field of a "+
					"configuration must declare its parameter name, or `-` if it is "+
					"derived and must not be settable",
				structType.Name(), field.Name)
		}
		if tag == "-" {
			continue
		}

		name, options := splitTag(tag)
		here := append(append([]int(nil), path...), i)

		if options["inline"] {
			// Flattened deliberately: stop_atr_mult reads better than
			// levels.stop_atr_mult, and the nesting is an implementation
			// detail of where the field happens to live.
			if err := walkParams(value.Field(i), here, specs); err != nil {
				return err
			}
			continue
		}

		kind, err := paramKind(field.Type)
		if err != nil {
			return fmt.Errorf("params: %s.%s: %w", structType.Name(), field.Name, err)
		}
		if name == "" {
			return fmt.Errorf("params: %s.%s has an empty parameter name", structType.Name(), field.Name)
		}

		*specs = append(*specs, ParamSpec{
			Name:    name,
			Kind:    kind,
			Default: formatParam(value.Field(i)),
			Step:    options.value("step"),
			path:    here,
		})
	}
	return nil
}

// tagOptions are the comma-separated settings after a parameter's name.
type tagOptions map[string]bool

func (o tagOptions) value(key string) string {
	for option := range o {
		if after, found := strings.CutPrefix(option, key+"="); found {
			return after
		}
	}
	return ""
}

// splitTag separates the parameter name from its options.
func splitTag(tag string) (string, tagOptions) {
	parts := strings.Split(tag, ",")
	options := tagOptions{}
	for _, option := range parts[1:] {
		options[strings.TrimSpace(option)] = true
	}
	return strings.TrimSpace(parts[0]), options
}

// paramKind maps a Go type onto the kind reported to a human.
func paramKind(t reflect.Type) (ParamKind, error) {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return ParamInt, nil
	case reflect.Float32, reflect.Float64:
		return ParamFloat, nil
	case reflect.Bool:
		return ParamBool, nil
	case reflect.String:
		return ParamString, nil
	case reflect.Slice:
		if t.Elem().Kind() == reflect.String {
			return ParamList, nil
		}
	}
	return "", fmt.Errorf("%s is not a settable parameter type", t)
}

// fieldByPath walks an index path to a nested field.
func fieldByPath(value reflect.Value, path []int) reflect.Value {
	for _, index := range path {
		value = value.Field(index)
	}
	return value
}

// setParam parses raw and writes it to the field.
func setParam(root reflect.Value, spec ParamSpec, raw string) error {
	field := fieldByPath(root, spec.path)
	raw = strings.TrimSpace(raw)

	switch spec.Kind {
	case ParamInt:
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("%s: %q is not a whole number", spec.Name, raw)
		}
		field.SetInt(parsed)

	case ParamFloat:
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("%s: %q is not a number", spec.Name, raw)
		}
		field.SetFloat(parsed)

	case ParamBool:
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("%s: %q is not true or false", spec.Name, raw)
		}
		field.SetBool(parsed)

	case ParamString:
		field.SetString(raw)

	case ParamList:
		items := strings.Split(raw, ",")
		slice := reflect.MakeSlice(field.Type(), 0, len(items))
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			element := reflect.New(field.Type().Elem()).Elem()
			element.SetString(item)
			slice = reflect.Append(slice, element)
		}
		field.Set(slice)

	default:
		return fmt.Errorf("%s: %s has no parser", spec.Name, spec.Kind)
	}
	return nil
}

// stepValue moves a field by its declared step.
func stepValue(field reflect.Value, spec ParamSpec, direction int) error {
	switch spec.Kind {
	case ParamInt:
		step, err := strconv.ParseInt(spec.Step, 10, 64)
		if err != nil {
			return fmt.Errorf("%s: step %q is not a whole number", spec.Name, spec.Step)
		}
		field.SetInt(field.Int() + step*int64(direction))
		return nil

	case ParamFloat:
		step, err := strconv.ParseFloat(spec.Step, 64)
		if err != nil {
			return fmt.Errorf("%s: step %q is not a number", spec.Name, spec.Step)
		}
		field.SetFloat(field.Float() + step*float64(direction))
		return nil

	default:
		return fmt.Errorf("%s is a %s and has no neighbouring value", spec.Name, spec.Kind)
	}
}

// formatParam renders a field's value the way it would be typed.
func formatParam(field reflect.Value) string {
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(field.Int(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(field.Float(), 'g', -1, 64)
	case reflect.Bool:
		return strconv.FormatBool(field.Bool())
	case reflect.String:
		return field.String()
	case reflect.Slice:
		items := make([]string, 0, field.Len())
		for i := range field.Len() {
			items = append(items, field.Index(i).String())
		}
		return strings.Join(items, ",")
	default:
		return fmt.Sprintf("%v", field.Interface())
	}
}
