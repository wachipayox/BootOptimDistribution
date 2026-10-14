package revision

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
)

// JVMOption is one launcher JVM parameter distributed by a profile (schema_version 3).
// Launchers render Template with the chosen value, re-check the argument against their own
// allowlist and skip it when the platform, Java version or VM does not match.
type JVMOption struct {
	ID        string                       `json:"id"`
	Label     string                       `json:"label"`
	Template  string                       `json:"template"`
	Type      string                       `json:"type"`
	Min       *int64                       `json:"min,omitempty"`
	Max       *int64                       `json:"max,omitempty"`
	Values    []string                     `json:"values,omitempty"`
	Help      string                       `json:"help"`
	Java      JavaRange                    `json:"java"`
	VM        []string                     `json:"vm,omitempty"`
	Platforms map[string]JVMOptionPlatform `json:"platforms"`
}

// JavaRange is an inclusive Java feature-release range; a nil Max means no upper bound.
type JavaRange struct {
	Min int  `json:"min"`
	Max *int `json:"max,omitempty"`
}

// JVMOptionPlatform holds the defaults for one platform. Platforms never share values: an
// option valid on PC and Android declares both entries, each with its own evidence.
type JVMOptionPlatform struct {
	DefaultEnabled bool           `json:"default_enabled"`
	DefaultValue   any            `json:"default_value,omitempty"`
	Auto           *JVMOptionAuto `json:"auto,omitempty"`
	Evidence       JVMEvidence    `json:"evidence"`
}

type JVMEvidence struct {
	URL     string `json:"url"`
	Summary string `json:"summary,omitempty"`
}

// JVMOptionAuto lets the launcher decide from machine facts. The first matching rule wins;
// Otherwise applies when none match.
type JVMOptionAuto struct {
	Rules     []JVMAutoRule   `json:"rules"`
	Otherwise JVMAutoDecision `json:"otherwise"`
}

type JVMAutoRule struct {
	When    map[string]JVMFactRange `json:"when"`
	Enabled bool                    `json:"enabled"`
	Value   any                     `json:"value,omitempty"`
}

type JVMAutoDecision struct {
	Enabled bool `json:"enabled"`
	Value   any  `json:"value,omitempty"`
}

// JVMFactRange is an inclusive range over one machine fact; at least one bound is set.
type JVMFactRange struct {
	Min *int64 `json:"min,omitempty"`
	Max *int64 `json:"max,omitempty"`
}

const (
	JVMOptionTypeBool = "bool"
	JVMOptionTypeInt  = "int"
	JVMOptionTypeEnum = "enum"

	jvmValuePlaceholder = "{value}"
	maxJVMOptions       = 64
	maxJVMArgumentBytes = 256
)

// JVMOptionPlatforms are the launcher platforms a profile option can target.
var JVMOptionPlatforms = []string{"pc", "android"}

// JVMOptionVMs are the JVM implementations an option can be restricted to.
var JVMOptionVMs = []string{"hotspot", "openj9"}

// JVMAutoFacts are the machine facts automatic rules may test. Launchers that do not know a
// fact must treat the option as not applicable rather than guessing.
var JVMAutoFacts = []string{"logical_cpus", "memory_mib"}

// AllowedJVMSystemProperties lists the -D keys a profile may set. It is intentionally empty;
// adding a key requires the same one-line change in every launcher plus a test vector in
// docs/jvm-options-vectors.json.
var AllowedJVMSystemProperties = []string{}

// deniedJVMXXFlags are -XX names that read option files, hide unknown-option failures, load
// an alternative compiler, write unbounded logs, pause the VM or open management access.
var deniedJVMXXFlags = map[string]struct{}{
	"Flags":                       {},
	"VMOptionsFile":               {},
	"IgnoreUnrecognizedVMOptions": {},
	"EnableJVMCI":                 {},
	"EnableJVMCIProduct":          {},
	"UseJVMCICompiler":            {},
	"UseJVMCINativeLibrary":       {},
	"LogVMOutput":                 {},
	"ShowMessageBoxOnError":       {},
	"PauseAtStartup":              {},
	"PauseAtExit":                 {},
	"ManagementServer":            {},
}

// JVMFlagKey returns the identity HotSpot uses to resolve repeated options: the last
// occurrence of the same key wins. It is used to detect profile/user conflicts.
func JVMFlagKey(arg string) (string, error) {
	if err := ValidateJVMArgument(arg); err != nil {
		return "", err
	}
	return jvmFlagKeyUnchecked(arg), nil
}

func jvmFlagKeyUnchecked(arg string) string {
	switch {
	case strings.HasPrefix(arg, "-XX:"):
		body := strings.TrimPrefix(arg, "-XX:")
		if strings.HasPrefix(body, "+") || strings.HasPrefix(body, "-") {
			return "XX:" + body[1:]
		}
		name, _, _ := strings.Cut(body, "=")
		return "XX:" + name
	case strings.HasPrefix(arg, "-D"):
		key, _, _ := strings.Cut(strings.TrimPrefix(arg, "-D"), "=")
		return "D:" + key
	default:
		return arg[1:4]
	}
}

// ValidateJVMArgument applies the launcher allowlist to one rendered argument.
func ValidateJVMArgument(arg string) error {
	if arg == "" || len(arg) > maxJVMArgumentBytes {
		return errors.New("argument is empty or longer than 256 bytes")
	}
	for i := 0; i < len(arg); i++ {
		if arg[i] <= ' ' || arg[i] > '~' {
			return errors.New("argument must be printable ASCII without spaces")
		}
	}
	switch {
	case strings.HasPrefix(arg, "-XX:"):
		return validateXXArgument(strings.TrimPrefix(arg, "-XX:"))
	case strings.HasPrefix(arg, "-Xss"), strings.HasPrefix(arg, "-Xms"), strings.HasPrefix(arg, "-Xmx"):
		if !validJVMSize(arg[4:]) {
			return errors.New("-Xss/-Xms/-Xmx require a size such as 512m")
		}
		return nil
	case strings.HasPrefix(arg, "-D"):
		key, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "-D"), "=")
		if !hasValue || !containsString(AllowedJVMSystemProperties, key) {
			return fmt.Errorf("system property %q is not in the allowlist", key)
		}
		if strings.ContainsAny(value, `"'\`) {
			return errors.New("system property value contains quotes or backslashes")
		}
		return nil
	default:
		return errors.New("only -XX:, -Xss, -Xms, -Xmx and allowlisted -D arguments are accepted")
	}
}

func validateXXArgument(body string) error {
	var name string
	if strings.HasPrefix(body, "+") || strings.HasPrefix(body, "-") {
		name = body[1:]
	} else {
		var value string
		var hasValue bool
		name, value, hasValue = strings.Cut(body, "=")
		if !hasValue || !validJVMNumber(value) {
			return errors.New("-XX values must be a number or size; text values are not accepted")
		}
	}
	if !validJVMFlagName(name) {
		return errors.New("-XX flag name is malformed")
	}
	if _, denied := deniedJVMXXFlags[name]; denied {
		return fmt.Errorf("-XX:%s is not allowed", name)
	}
	return nil
}

func validJVMFlagName(name string) bool {
	if name == "" || len(name) > 80 || !((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z')) {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

// validJVMNumber accepts an integer, a decimal or a size with a k/m/g/t suffix.
func validJVMNumber(value string) bool {
	if validJVMSize(value) {
		return true
	}
	whole, fraction, ok := strings.Cut(value, ".")
	return ok && allDigits(whole) && allDigits(fraction)
}

func validJVMSize(value string) bool {
	if value == "" {
		return false
	}
	switch value[len(value)-1] {
	case 'k', 'K', 'm', 'M', 'g', 'G', 't', 'T':
		value = value[:len(value)-1]
	}
	return allDigits(value)
}

func allDigits(value string) bool {
	if value == "" || len(value) > 19 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

// RenderJVMOption returns the argument for a chosen value (ignored for bool options).
func RenderJVMOption(option JVMOption, value any) (string, error) {
	if option.Type == JVMOptionTypeBool {
		return option.Template, nil
	}
	text, ok := jvmValueText(option, value)
	if !ok {
		return "", errors.New("value is outside the option's type or range")
	}
	return strings.Replace(option.Template, jvmValuePlaceholder, text, 1), nil
}

func jvmValueText(option JVMOption, value any) (string, bool) {
	switch option.Type {
	case JVMOptionTypeInt:
		number, ok := jvmInteger(value)
		if !ok || option.Min == nil || option.Max == nil || number < *option.Min || number > *option.Max {
			return "", false
		}
		return fmt.Sprintf("%d", number), true
	case JVMOptionTypeEnum:
		text, ok := value.(string)
		return text, ok && containsString(option.Values, text)
	default:
		return "", false
	}
}

func jvmInteger(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed != math.Trunc(typed) || math.Abs(typed) > math.MaxInt32 {
			return 0, false
		}
		return int64(typed), true
	case int64:
		return typed, typed >= math.MinInt32 && typed <= math.MaxInt32
	case int:
		return int64(typed), typed >= math.MinInt32 && typed <= math.MaxInt32
	default:
		return 0, false
	}
}

func validateJVMOptions(m Manifest) error {
	if len(m.JVMOptions) > maxJVMOptions || len(m.RemoveJVMOptions) > maxJVMOptions {
		return errors.New("jvm_options and remove_jvm_options are limited to 64 entries")
	}
	if m.Base == nil && len(m.RemoveJVMOptions) != 0 {
		return errors.New("root manifest cannot remove jvm_options")
	}
	ids := make(map[string]struct{}, len(m.JVMOptions))
	for i, option := range m.JVMOptions {
		if err := validateJVMOption(option); err != nil {
			return fmt.Errorf("jvm_options[%d]: %w", i, err)
		}
		if _, exists := ids[option.ID]; exists {
			return fmt.Errorf("jvm_options[%d] duplicates id %q", i, option.ID)
		}
		ids[option.ID] = struct{}{}
	}
	removed := make(map[string]struct{}, len(m.RemoveJVMOptions))
	for i, id := range m.RemoveJVMOptions {
		if !validJVMOptionID(id) {
			return fmt.Errorf("remove_jvm_options[%d] is invalid", i)
		}
		if _, exists := removed[id]; exists {
			return fmt.Errorf("remove_jvm_options[%d] duplicates id %q", i, id)
		}
		if _, exists := ids[id]; exists {
			return fmt.Errorf("jvm option %q is both published and removed", id)
		}
		removed[id] = struct{}{}
	}
	return checkJVMFlagCollisions(m.JVMOptions)
}

func validateJVMOption(option JVMOption) error {
	if !validJVMOptionID(option.ID) {
		return errors.New("id must match ^[a-z0-9][a-z0-9-]{0,63}$")
	}
	if option.Label == "" || len(option.Label) > 64 || strings.ContainsAny(option.Label, "\r\n") {
		return errors.New("label must be 1..64 bytes on one line")
	}
	if option.Help == "" || len(option.Help) > 1024 {
		return errors.New("help must be 1..1024 bytes")
	}
	if option.Java.Min < 8 || option.Java.Min > 1000 || (option.Java.Max != nil && *option.Java.Max < option.Java.Min) {
		return errors.New("java range must start at 8 or later and not end before it starts")
	}
	if err := validateStringSet(option.VM, JVMOptionVMs, "vm"); err != nil {
		return err
	}
	if strings.HasPrefix(option.Template, "-XX:") && len(option.VM) == 0 {
		return errors.New("-XX options must name the JVM implementations they apply to")
	}
	if err := validateJVMOptionTemplate(option); err != nil {
		return err
	}
	if len(option.Platforms) == 0 {
		return errors.New("platforms must contain pc and/or android")
	}
	for _, platform := range sortedPlatformKeys(option.Platforms) {
		if !containsString(JVMOptionPlatforms, platform) {
			return fmt.Errorf("platform %q is not supported", platform)
		}
		if err := validateJVMOptionPlatform(option, option.Platforms[platform]); err != nil {
			return fmt.Errorf("platforms.%s: %w", platform, err)
		}
	}
	return nil
}

func validateJVMOptionTemplate(option JVMOption) error {
	placeholders := strings.Count(option.Template, jvmValuePlaceholder)
	stripped := strings.ReplaceAll(option.Template, jvmValuePlaceholder, "")
	if strings.ContainsAny(stripped, "{}") {
		return errors.New("template may only contain the {value} placeholder")
	}
	var rendered []string
	switch option.Type {
	case JVMOptionTypeBool:
		if placeholders != 0 || option.Min != nil || option.Max != nil || len(option.Values) != 0 {
			return errors.New("bool options use a fixed template without {value}, min, max or values")
		}
		rendered = []string{option.Template}
	case JVMOptionTypeInt:
		if placeholders != 1 || option.Min == nil || option.Max == nil || len(option.Values) != 0 {
			return errors.New("int options need one {value}, min and max, and no values")
		}
		if *option.Min > *option.Max || *option.Min < math.MinInt32 || *option.Max > math.MaxInt32 {
			return errors.New("int range is invalid")
		}
		for _, bound := range []int64{*option.Min, *option.Max} {
			rendered = append(rendered, strings.Replace(option.Template, jvmValuePlaceholder, fmt.Sprintf("%d", bound), 1))
		}
	case JVMOptionTypeEnum:
		if placeholders != 1 || option.Min != nil || option.Max != nil || len(option.Values) == 0 || len(option.Values) > 16 {
			return errors.New("enum options need one {value} and 1..16 values, without min or max")
		}
		seen := make(map[string]struct{}, len(option.Values))
		for _, value := range option.Values {
			if !validEnumValue(value) {
				return fmt.Errorf("enum value %q is invalid", value)
			}
			if _, exists := seen[value]; exists {
				return fmt.Errorf("enum value %q is duplicated", value)
			}
			seen[value] = struct{}{}
			rendered = append(rendered, strings.Replace(option.Template, jvmValuePlaceholder, value, 1))
		}
	default:
		return errors.New("type must be bool, int or enum")
	}
	key := ""
	for _, arg := range rendered {
		if err := ValidateJVMArgument(arg); err != nil {
			return fmt.Errorf("template renders %q: %w", arg, err)
		}
		if next := jvmFlagKeyUnchecked(arg); key == "" {
			key = next
		} else if next != key {
			return errors.New("every value must render the same JVM flag")
		}
	}
	return nil
}

func validateJVMOptionPlatform(option JVMOption, platform JVMOptionPlatform) error {
	if err := validateJVMDecisionValue(option, platform.DefaultValue, true); err != nil {
		return fmt.Errorf("default_value: %w", err)
	}
	if err := validateEvidence(platform.Evidence); err != nil {
		return err
	}
	if platform.Auto == nil {
		return nil
	}
	if len(platform.Auto.Rules) == 0 || len(platform.Auto.Rules) > 16 {
		return errors.New("auto needs 1..16 rules")
	}
	for i, rule := range platform.Auto.Rules {
		if len(rule.When) == 0 {
			return fmt.Errorf("auto.rules[%d].when must test at least one fact", i)
		}
		for fact, bounds := range rule.When {
			if !containsString(JVMAutoFacts, fact) {
				return fmt.Errorf("auto.rules[%d] uses unknown fact %q", i, fact)
			}
			if bounds.Min == nil && bounds.Max == nil {
				return fmt.Errorf("auto.rules[%d].when.%s needs min or max", i, fact)
			}
			if (bounds.Min != nil && (*bounds.Min < 0 || *bounds.Min > 1<<30)) || (bounds.Max != nil && (*bounds.Max < 0 || *bounds.Max > 1<<30)) ||
				(bounds.Min != nil && bounds.Max != nil && *bounds.Min > *bounds.Max) {
				return fmt.Errorf("auto.rules[%d].when.%s range is invalid", i, fact)
			}
		}
		if err := validateJVMDecisionValue(option, rule.Value, rule.Enabled); err != nil {
			return fmt.Errorf("auto.rules[%d]: %w", i, err)
		}
	}
	if err := validateJVMDecisionValue(option, platform.Auto.Otherwise.Value, platform.Auto.Otherwise.Enabled); err != nil {
		return fmt.Errorf("auto.otherwise: %w", err)
	}
	return nil
}

// validateJVMDecisionValue checks a value against the option type; required is set when
// the decision enables a non-bool option.
func validateJVMDecisionValue(option JVMOption, value any, required bool) error {
	if option.Type == JVMOptionTypeBool {
		if value != nil {
			return errors.New("bool options do not take a value")
		}
		return nil
	}
	if value == nil {
		if required {
			return errors.New("a value is required")
		}
		return nil
	}
	if _, ok := jvmValueText(option, value); !ok {
		return errors.New("value is outside the option's type or range")
	}
	return nil
}

func validateEvidence(evidence JVMEvidence) error {
	if len(evidence.URL) == 0 || len(evidence.URL) > 512 || strings.ContainsAny(evidence.URL, " \t\r\n") {
		return errors.New("evidence.url must be 1..512 bytes without spaces")
	}
	parsed, err := url.Parse(evidence.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return errors.New("evidence.url must be an https URL")
	}
	if len(evidence.Summary) > 512 {
		return errors.New("evidence.summary exceeds 512 bytes")
	}
	return nil
}

func validJVMOptionID(id string) bool {
	if id == "" || len(id) > 64 || !isLowerAlphaNum(id[0]) {
		return false
	}
	for i := 1; i < len(id); i++ {
		if !isLowerAlphaNum(id[i]) && id[i] != '-' {
			return false
		}
	}
	return true
}

func validEnumValue(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '+' || c == '-') {
			return false
		}
	}
	return true
}

func validateStringSet(values, allowed []string, name string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !containsString(allowed, value) {
			return fmt.Errorf("%s value %q is not supported", name, value)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%s value %q is duplicated", name, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

// checkJVMFlagCollisions rejects two options that set the same JVM flag on one platform,
// because only the last one would take effect.
func checkJVMFlagCollisions(options []JVMOption) error {
	owners := make(map[string]string)
	for _, option := range options {
		key := jvmOptionFlagKey(option)
		for platform := range option.Platforms {
			slot := platform + "\x00" + key
			if owner, exists := owners[slot]; exists && owner != option.ID {
				return fmt.Errorf("jvm options %q and %q both set %s on %s", owner, option.ID, key, platform)
			}
			owners[slot] = option.ID
		}
	}
	return nil
}

func jvmOptionFlagKey(option JVMOption) string {
	sample := option.Template
	switch option.Type {
	case JVMOptionTypeInt:
		if option.Min != nil {
			sample = strings.Replace(sample, jvmValuePlaceholder, fmt.Sprintf("%d", *option.Min), 1)
		}
	case JVMOptionTypeEnum:
		if len(option.Values) > 0 {
			sample = strings.Replace(sample, jvmValuePlaceholder, option.Values[0], 1)
		}
	}
	if len(sample) < 4 {
		return sample
	}
	return jvmFlagKeyUnchecked(sample)
}

// EffectiveJVMOptions resolves a chain (target first, root last) oldest to newest: a child
// replaces an option with the same id in place and remove_jvm_options withdraws inherited
// ones. The result keeps declaration order so launchers can show options as published.
func EffectiveJVMOptions(chain []*VerifiedRevision) ([]JVMOption, error) {
	var out []JVMOption
	index := make(map[string]int)
	for position := len(chain) - 1; position >= 0; position-- {
		manifest := chain[position].manifest
		for _, id := range manifest.RemoveJVMOptions {
			at, exists := index[id]
			if !exists {
				return nil, fmt.Errorf("remove_jvm_options names %q, which the parent profile does not define", id)
			}
			out = append(out[:at], out[at+1:]...)
			delete(index, id)
			for other, otherAt := range index {
				if otherAt > at {
					index[other] = otherAt - 1
				}
			}
		}
		for _, option := range manifest.JVMOptions {
			if at, exists := index[option.ID]; exists {
				out[at] = cloneJVMOption(option)
				continue
			}
			index[option.ID] = len(out)
			out = append(out, cloneJVMOption(option))
		}
	}
	if err := checkJVMFlagCollisions(out); err != nil {
		return nil, err
	}
	return out, nil
}

func sortedPlatformKeys(platforms map[string]JVMOptionPlatform) []string {
	keys := make([]string, 0, len(platforms))
	for key := range platforms {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func cloneJVMOption(option JVMOption) JVMOption {
	out := option
	out.Min = cloneInt64Ptr(option.Min)
	out.Max = cloneInt64Ptr(option.Max)
	out.Values = append([]string(nil), option.Values...)
	out.VM = append([]string(nil), option.VM...)
	if option.Java.Max != nil {
		max := *option.Java.Max
		out.Java.Max = &max
	}
	if option.Platforms != nil {
		out.Platforms = make(map[string]JVMOptionPlatform, len(option.Platforms))
		for key, platform := range option.Platforms {
			copy := platform
			copy.DefaultValue = cloneJSONValue(platform.DefaultValue)
			if platform.Auto != nil {
				auto := JVMOptionAuto{Otherwise: platform.Auto.Otherwise}
				auto.Otherwise.Value = cloneJSONValue(platform.Auto.Otherwise.Value)
				for _, rule := range platform.Auto.Rules {
					ruleCopy := JVMAutoRule{Enabled: rule.Enabled, Value: cloneJSONValue(rule.Value), When: make(map[string]JVMFactRange, len(rule.When))}
					for fact, bounds := range rule.When {
						ruleCopy.When[fact] = JVMFactRange{Min: cloneInt64Ptr(bounds.Min), Max: cloneInt64Ptr(bounds.Max)}
					}
					auto.Rules = append(auto.Rules, ruleCopy)
				}
				copy.Auto = &auto
			}
			out.Platforms[key] = copy
		}
	}
	return out
}

func cloneInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
