// Package icalsplit partitions the VEVENTs from two iCalendar feeds into
// first-only, second-only, and shared calendars. Event comparison ignores
// DTSTAMP, which commonly differs between otherwise identical source events.
package icalsplit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Output contains three complete iCalendar documents. Shared events are taken
// from the first calendar, so their original properties (including DTSTAMP)
// are retained from that source.
type Output struct {
	FirstOnly  []byte
	SecondOnly []byte
	Shared     []byte
}

// Split compares two complete VCALENDAR documents. It de-duplicates repeated
// matching events within each input. Two events match when their content is
// identical after normalizing property/parameter ordering and ignoring the
// event's DTSTAMP property. UID, times, recurrence rules, exceptions, alarms,
// and all other event data remain part of the comparison.
//
// The output preserves the first calendar's calendar-level properties, merges
// VTIMEZONE components from both inputs (first definition wins for a TZID),
// and emits the selected VEVENTs. Other top-level component types are not
// copied because these outputs are intended to partition appointments.
func Split(firstICS, secondICS []byte) (Output, error) {
	first, err := parseCalendar(firstICS)
	if err != nil {
		return Output{}, fmt.Errorf("parse first calendar: %w", err)
	}
	second, err := parseCalendar(secondICS)
	if err != nil {
		return Output{}, fmt.Errorf("parse second calendar: %w", err)
	}

	firstEvents := uniqueEvents(first)
	secondEvents := uniqueEvents(second)
	firstSet := make(map[string]struct{}, len(firstEvents))
	secondSet := make(map[string]struct{}, len(secondEvents))
	for _, event := range firstEvents {
		firstSet[event.signature] = struct{}{}
	}
	for _, event := range secondEvents {
		secondSet[event.signature] = struct{}{}
	}

	var firstOnly, secondOnly, shared []eventEntry
	for _, event := range firstEvents {
		if _, ok := secondSet[event.signature]; ok {
			shared = append(shared, event)
		} else {
			firstOnly = append(firstOnly, event)
		}
	}
	for _, event := range secondEvents {
		if _, ok := firstSet[event.signature]; !ok {
			secondOnly = append(secondOnly, event)
		}
	}

	timezones := mergeTimezones(first, second)
	return Output{
		FirstOnly:  renderCalendar(first, timezones, firstOnly),
		SecondOnly: renderCalendar(first, timezones, secondOnly),
		Shared:     renderCalendar(first, timezones, shared),
	}, nil
}

type logicalLine struct {
	value    string
	physical []string // Original physical lines, including any folded lines.
}

type component struct {
	name       string
	properties []logicalLine
	children   []*component
	raw        []byte // Original component lines, normalized to CRLF.
}

type eventEntry struct {
	component *component
	signature string
	dtstamp   string
}

func parseCalendar(data []byte) (*component, error) {
	lines, err := unfoldLines(data)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, errors.New("empty calendar")
	}
	index := 0
	root, err := parseComponent(lines, &index)
	if err != nil {
		return nil, err
	}
	if root.name != "VCALENDAR" {
		return nil, fmt.Errorf("expected VCALENDAR, got %s", root.name)
	}
	if index != len(lines) {
		return nil, fmt.Errorf("unexpected content after VCALENDAR at line %d", index+1)
	}
	return root, nil
}

func unfoldLines(data []byte) ([]logicalLine, error) {
	s := strings.TrimPrefix(string(data), "\uFEFF")
	// Real-world feeds commonly use either CRLF (the standard) or LF.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	physical := strings.Split(s, "\n")
	lines := make([]logicalLine, 0, len(physical))
	for _, line := range physical {
		if line == "" { // Ignore empty separators and the final newline.
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if len(lines) == 0 {
				return nil, errors.New("folded line before any content line")
			}
			last := &lines[len(lines)-1]
			last.value += line[1:]
			last.physical = append(last.physical, line)
			continue
		}
		lines = append(lines, logicalLine{value: line, physical: []string{line}})
	}
	return lines, nil
}

func parseComponent(lines []logicalLine, index *int) (*component, error) {
	if *index >= len(lines) {
		return nil, errors.New("expected BEGIN component, reached end of input")
	}
	start := *index
	property, value, err := parseContentLine(lines[*index].value)
	if err != nil {
		return nil, fmt.Errorf("line %d: %w", *index+1, err)
	}
	if property != "BEGIN" {
		return nil, fmt.Errorf("line %d: expected BEGIN, got %s", *index+1, property)
	}
	name := strings.ToUpper(strings.TrimSpace(value))
	if name == "" {
		return nil, fmt.Errorf("line %d: empty component name", *index+1)
	}
	c := &component{name: name}
	*index++

	for *index < len(lines) {
		lineNo := *index + 1
		prop, val, err := parseContentLine(lines[*index].value)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		switch prop {
		case "BEGIN":
			child, err := parseComponent(lines, index)
			if err != nil {
				return nil, err
			}
			c.children = append(c.children, child)
		case "END":
			endName := strings.ToUpper(strings.TrimSpace(val))
			if endName != c.name {
				return nil, fmt.Errorf("line %d: END:%s does not match BEGIN:%s", lineNo, endName, c.name)
			}
			*index++
			c.raw = rawLines(lines[start:*index])
			return c, nil
		default:
			c.properties = append(c.properties, lines[*index])
			*index++
		}
	}
	return nil, fmt.Errorf("component %s has no matching END:%s", c.name, c.name)
}

func rawLines(lines []logicalLine) []byte {
	var out bytes.Buffer
	for _, line := range lines {
		for _, physical := range line.physical {
			out.WriteString(physical)
			out.WriteString("\r\n")
		}
	}
	return out.Bytes()
}

// parseContentLine returns an uppercase property name and the property value.
// It splits at the first colon outside a quoted parameter value.
func parseContentLine(line string) (name, value string, err error) {
	colon := firstUnquotedColon(line)
	if colon < 0 {
		return "", "", fmt.Errorf("malformed content line %q: missing colon", line)
	}
	header := line[:colon]
	semi := strings.IndexByte(header, ';')
	if semi >= 0 {
		header = header[:semi]
	}
	name = strings.ToUpper(strings.TrimSpace(header))
	if name == "" {
		return "", "", fmt.Errorf("malformed content line %q: empty property name", line)
	}
	return name, line[colon+1:], nil
}

func firstUnquotedColon(s string) int {
	quoted := false
	escaped := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			quoted = !quoted
			continue
		}
		if ch == ':' && !quoted {
			return i
		}
	}
	return -1
}

func uniqueEvents(calendar *component) []eventEntry {
	var result []eventEntry
	indices := make(map[string]int)
	for _, child := range calendar.children {
		if child.name != "VEVENT" {
			continue
		}
		signature := canonicalComponent(child, true)
		entry := eventEntry{
			component: child,
			signature: signature,
			dtstamp:   propertyValue(child, "DTSTAMP"),
		}
		if existingIndex, ok := indices[signature]; ok {
			// Repeated copies within one feed collapse to one appointment. Use
			// the newest DTSTAMP as the representative for that feed.
			if entry.dtstamp > result[existingIndex].dtstamp {
				result[existingIndex] = entry
			}
			continue
		}
		indices[signature] = len(result)
		result = append(result, entry)
	}
	return result
}

func propertyValue(c *component, target string) string {
	for _, line := range c.properties {
		name, value, err := parseContentLine(line.value)
		if err == nil && name == target {
			return value
		}
	}
	return ""
}

type canonicalNode struct {
	Name       string   `json:"name"`
	Properties []string `json:"properties"`
	Children   []string `json:"children"`
}

type canonicalProperty struct {
	Name       string   `json:"name"`
	Parameters []string `json:"parameters,omitempty"`
	Value      string   `json:"value"`
}

func canonicalComponent(c *component, ignoreOwnDTStamp bool) string {
	props := make([]string, 0, len(c.properties))
	for _, line := range c.properties {
		prop, err := normalizeProperty(line.value)
		if err != nil {
			// Parsing already checked for a colon, but retain a stable fallback
			// if an unusual property cannot be normalized.
			props = append(props, line.value)
			continue
		}
		if ignoreOwnDTStamp && prop.Name == "DTSTAMP" {
			continue
		}
		encoded, _ := json.Marshal(prop)
		props = append(props, string(encoded))
	}
	sort.Strings(props)

	children := make([]string, 0, len(c.children))
	for _, child := range c.children {
		children = append(children, canonicalComponent(child, false))
	}
	sort.Strings(children)

	encoded, _ := json.Marshal(canonicalNode{
		Name:       c.name,
		Properties: props,
		Children:   children,
	})
	return string(encoded)
}

func normalizeProperty(line string) (canonicalProperty, error) {
	colon := firstUnquotedColon(line)
	if colon < 0 {
		return canonicalProperty{}, fmt.Errorf("missing colon in %q", line)
	}
	header, value := line[:colon], line[colon+1:]
	parts := splitUnquoted(header, ';')
	if len(parts) == 0 {
		return canonicalProperty{}, fmt.Errorf("missing property name in %q", line)
	}
	result := canonicalProperty{
		Name:  strings.ToUpper(strings.TrimSpace(parts[0])),
		Value: value,
	}
	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if equal := strings.IndexByte(part, '='); equal >= 0 {
			key := strings.ToUpper(strings.TrimSpace(part[:equal]))
			paramValue := strings.TrimSpace(part[equal+1:])
			result.Parameters = append(result.Parameters, key+"="+paramValue)
		} else {
			result.Parameters = append(result.Parameters, strings.ToUpper(part))
		}
	}
	sort.Strings(result.Parameters)
	return result, nil
}

func splitUnquoted(s string, delimiter byte) []string {
	var parts []string
	start := 0
	quoted := false
	escaped := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			quoted = !quoted
			continue
		}
		if ch == delimiter && !quoted {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func mergeTimezones(first, second *component) []*component {
	var result []*component
	seen := make(map[string]struct{})
	for _, calendar := range []*component{first, second} {
		for _, child := range calendar.children {
			if child.name != "VTIMEZONE" {
				continue
			}
			key := timezoneKey(child)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, child)
		}
	}
	return result
}

func timezoneKey(c *component) string {
	for _, line := range c.properties {
		name, value, err := parseContentLine(line.value)
		if err == nil && name == "TZID" {
			return "TZID:" + value
		}
	}
	return "VTIMEZONE:" + canonicalComponent(c, false)
}

func renderCalendar(base *component, timezones []*component, events []eventEntry) []byte {
	var out bytes.Buffer
	out.WriteString("BEGIN:VCALENDAR\r\n")

	hasVersion, hasProductID := false, false
	for _, property := range base.properties {
		name, _, err := parseContentLine(property.value)
		if err == nil {
			switch name {
			case "VERSION":
				hasVersion = true
			case "PRODID":
				hasProductID = true
			}
		}
		out.Write(rawLines([]logicalLine{property}))
	}
	// VERSION and PRODID are required VCALENDAR properties; add safe defaults
	// for stripped-down source files that omit them.
	if !hasProductID {
		out.WriteString("PRODID:-//ical-splitter//EN\r\n")
	}
	if !hasVersion {
		out.WriteString("VERSION:2.0\r\n")
	}

	for _, timezone := range timezones {
		out.Write(timezone.raw)
	}
	for _, event := range events {
		out.Write(event.component.raw)
	}
	out.WriteString("END:VCALENDAR\r\n")
	return out.Bytes()
}
