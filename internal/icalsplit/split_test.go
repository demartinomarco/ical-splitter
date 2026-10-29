package icalsplit

import (
	"fmt"
	"strings"
	"testing"
)

func calendar(components ...string) []byte {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//ical-splitter tests//EN\r\n")
	for _, component := range components {
		b.WriteString(strings.ReplaceAll(component, "\n", "\r\n"))
		if !strings.HasSuffix(component, "\n") {
			b.WriteString("\r\n")
		}
	}
	b.WriteString("END:VCALENDAR\r\n")
	return []byte(b.String())
}

func event(uid, summary, dtstamp, extra string) string {
	stamp := ""
	if dtstamp != "" {
		stamp = fmt.Sprintf("DTSTAMP:%s\n", dtstamp)
	}
	return fmt.Sprintf("BEGIN:VEVENT\nUID:%s\nSUMMARY:%s\nDTSTART:20261027T140000Z\nDTEND:20261027T153000Z\n%s%sEND:VEVENT\n",
		uid, summary, stamp, extra)
}

func eventUIDs(t *testing.T, ics []byte) []string {
	t.Helper()
	root, err := parseCalendar(ics)
	if err != nil {
		t.Fatalf("output is not a parseable calendar: %v", err)
	}
	var uids []string
	for _, child := range root.children {
		if child.name == "VEVENT" {
			uids = append(uids, propertyValue(child, "UID"))
		}
	}
	return uids
}

func TestSplitPartitionsEventsIgnoringDTStamp(t *testing.T) {
	sharedFirst := "BEGIN:VEVENT\n" +
		"UID:shared@example.test\n" +
		"SUMMARY:2424187 - Natural Language Processing and Software Engineering (V)\\, Koziolek et al.\n" +
		"DTSTART;TZID=Europe/Berlin:20261027T140000\n" +
		"DTEND;TZID=Europe/Berlin:20261027T153000\n" +
		"SEQUENCE:0\n" +
		"DTSTAMP:20261028T210116\n" +
		"RRULE:FREQ=WEEKLY;UNTIL=20270216T153000\n" +
		"EXDATE;TZID=Europe/Berlin:20261229T140000\n" +
		"EXDATE;TZID=Europe/Berlin:20270105T140000\n" +
		"LOCATION:50.34 Raum -118 (UG)\n" +
		"DESCRIPTION:https://campus.example.test/event/one\n" +
		"URL:https://campus.example.test/event/one\n" +
		"END:VEVENT\n"
	sharedSecond := strings.Replace(sharedFirst, "DTSTAMP:20261028T210116", "DTSTAMP:20261028T210147", 1)

	first := calendar(sharedFirst, event("first-only@example.test", "Only in first", "20261028T210000", ""))
	second := calendar(sharedSecond, event("second-only@example.test", "Only in second", "20261028T220000", ""))

	got, err := Split(first, second)
	if err != nil {
		t.Fatalf("Split returned error: %v", err)
	}

	assertUIDs(t, eventUIDs(t, got.FirstOnly), []string{"first-only@example.test"})
	assertUIDs(t, eventUIDs(t, got.SecondOnly), []string{"second-only@example.test"})
	assertUIDs(t, eventUIDs(t, got.Shared), []string{"shared@example.test"})

	if !strings.Contains(string(got.Shared), "DTSTAMP:20261028T210116\r\n") {
		t.Fatal("shared output should preserve the event from the first calendar")
	}
	if strings.Contains(string(got.Shared), "DTSTAMP:20261028T210147") {
		t.Fatal("shared output unexpectedly contains the second calendar's DTSTAMP")
	}
	for name, output := range map[string][]byte{
		"first-only":  got.FirstOnly,
		"second-only": got.SecondOnly,
		"shared":      got.Shared,
	} {
		if !strings.HasPrefix(string(output), "BEGIN:VCALENDAR\r\n") || !strings.HasSuffix(string(output), "END:VCALENDAR\r\n") {
			t.Errorf("%s output is not wrapped in VCALENDAR", name)
		}
		if strings.Contains(strings.ReplaceAll(string(output), "\r\n", ""), "\n") {
			t.Errorf("%s output contains a bare LF line ending", name)
		}
	}
}

func TestSplitDoesNotMatchDifferentEventContent(t *testing.T) {
	first := calendar(event("same-uid@example.test", "First title", "20261028T210000", ""))
	second := calendar(event("same-uid@example.test", "Different title", "20261028T220000", ""))

	got, err := Split(first, second)
	if err != nil {
		t.Fatal(err)
	}
	assertUIDs(t, eventUIDs(t, got.FirstOnly), []string{"same-uid@example.test"})
	assertUIDs(t, eventUIDs(t, got.SecondOnly), []string{"same-uid@example.test"})
	assertUIDs(t, eventUIDs(t, got.Shared), nil)
}

func TestSplitNormalizesPropertyOrderAndFoldedLines(t *testing.T) {
	firstEvent := "BEGIN:VEVENT\n" +
		"UID:folded@example.test\n" +
		"SUMMARY:Folded title\n" +
		"DESCRIPTION:A description that is folded\n" +
		" across two physical lines\n" +
		"DTSTART;TZID=Europe/Berlin:20261027T140000\n" +
		"DTSTAMP:20261028T210116\n" +
		"END:VEVENT\n"
	secondEvent := "BEGIN:VEVENT\n" +
		"DTSTAMP:20261028T210147\n" +
		"DTSTART;TZID=Europe/Berlin:20261027T140000\n" +
		"DESCRIPTION:A description that is foldedacross two physical lines\n" +
		"SUMMARY:Folded title\n" +
		"UID:folded@example.test\n" +
		"END:VEVENT\n"

	got, err := Split(calendar(firstEvent), calendar(secondEvent))
	if err != nil {
		t.Fatal(err)
	}
	assertUIDs(t, eventUIDs(t, got.Shared), []string{"folded@example.test"})
	if !strings.Contains(string(got.Shared), "DESCRIPTION:A description that is folded\r\n across two physical lines\r\n") {
		t.Fatal("shared event's original folded content line was not preserved")
	}
}

func TestSplitMergesTimezonesWithoutDuplicates(t *testing.T) {
	tzBerlin := "BEGIN:VTIMEZONE\nTZID:Europe/Berlin\nX-LIC-LOCATION:Europe/Berlin\nEND:VTIMEZONE\n"
	tzParis := "BEGIN:VTIMEZONE\nTZID:Europe/Paris\nX-LIC-LOCATION:Europe/Paris\nEND:VTIMEZONE\n"
	first := calendar(tzBerlin, event("a@example.test", "A", "20261028T210000", ""))
	second := calendar(tzBerlin, tzParis, event("b@example.test", "B", "20261028T220000", ""))

	got, err := Split(first, second)
	if err != nil {
		t.Fatal(err)
	}
	for name, output := range map[string][]byte{
		"first-only":  got.FirstOnly,
		"second-only": got.SecondOnly,
		"shared":      got.Shared,
	} {
		countBerlin := strings.Count(string(output), "TZID:Europe/Berlin")
		countParis := strings.Count(string(output), "TZID:Europe/Paris")
		if countBerlin != 1 || countParis != 1 {
			t.Errorf("%s output timezone counts Berlin=%d Paris=%d; want one of each", name, countBerlin, countParis)
		}
	}
}

func TestSplitDeduplicatesRepeatedEventsWithinOneInput(t *testing.T) {
	old := event("repeat@example.test", "Repeated", "20261028T210116", "")
	newer := strings.Replace(old, "DTSTAMP:20261028T210116", "DTSTAMP:20261028T220000", 1)
	got, err := Split(calendar(old, newer), calendar(old))
	if err != nil {
		t.Fatal(err)
	}
	assertUIDs(t, eventUIDs(t, got.Shared), []string{"repeat@example.test"})
	if !strings.Contains(string(got.Shared), "DTSTAMP:20261028T220000\r\n") {
		t.Fatal("expected the newest DTSTAMP from repeated events in the first input")
	}
}

func TestSplitRejectsMalformedCalendars(t *testing.T) {
	valid := calendar(event("a@example.test", "A", "20261028T210000", ""))
	for _, malformed := range [][]byte{
		[]byte(""),
		[]byte("BEGIN:VCALENDAR\nVERSION:2.0\n"),
		[]byte("BEGIN:VEVENT\nUID:x\nEND:VEVENT\n"),
		[]byte("BEGIN:VCALENDAR\nVERSION:2.0\nEND:VEVENT\nEND:VCALENDAR\n"),
	} {
		if _, err := Split(valid, malformed); err == nil {
			t.Errorf("Split unexpectedly accepted malformed input: %q", malformed)
		}
	}
}

func assertUIDs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("UIDs = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("UIDs = %v, want %v", got, want)
		}
	}
}

func TestSplitRejectsMalformedFirstCalendar(t *testing.T) {
	if _, err := Split([]byte(""), calendar(event("a@example.test", "A", "20261028T210000", ""))); err == nil {
		t.Error("Split unexpectedly accepted a malformed first calendar")
	}
}

func TestSplitRejectsTrailingContentAfterVCalendar(t *testing.T) {
	first := calendar(event("a@example.test", "A", "20261028T210000", ""))
	second := []byte("BEGIN:VCALENDAR\nVERSION:2.0\nEND:VCALENDAR\nBEGIN:VEVENT\nUID:x\nEND:VEVENT\n")
	if _, err := Split(first, second); err == nil {
		t.Error("Split unexpectedly accepted content after END:VCALENDAR")
	}
}

func TestSplitRejectsFoldedFirstLine(t *testing.T) {
	first := calendar(event("a@example.test", "A", "20261028T210000", ""))
	second := []byte(" BEGIN:VCALENDAR\nEND:VCALENDAR\n")
	if _, err := Split(first, second); err == nil {
		t.Error("Split unexpectedly accepted a calendar starting with a folded line")
	}
}

func TestSplitRejectsStructurallyMalformedComponents(t *testing.T) {
	valid := calendar(event("a@example.test", "A", "20261028T210000", ""))
	tests := []struct {
		name    string
		invalid []byte
		wantErr string
	}{
		{
			name:    "first line has no colon",
			invalid: []byte("NO_COLON_HERE\nNOT_CALENDAR\n"),
			wantErr: "malformed content line",
		},
		{
			name:    "first line is not BEGIN",
			invalid: []byte("X-PROP:1\nBEGIN:VCALENDAR\nEND:VCALENDAR\n"),
			wantErr: "expected BEGIN",
		},
		{
			name:    "empty component name",
			invalid: []byte("BEGIN:\r\nEND:VCALENDAR\r\n"),
			wantErr: "empty component name",
		},
		{
			name:    "nested component parse error",
			invalid: []byte("BEGIN:VCALENDAR\nBEGIN:VEVENT\nBEGIN:VALARM\nEND:VEVENT\nEND:VCALENDAR\n"),
			wantErr: "END:VEVENT does not match BEGIN:VALARM",
		},
		{
			name:    "content line without colon",
			invalid: []byte("BEGIN:VCALENDAR\nNO_COLON\nEND:VCALENDAR\n"),
			wantErr: "malformed content line",
		},
		{
			name:    "empty property name",
			invalid: []byte("BEGIN:VCALENDAR\n:orphan\nEND:VCALENDAR\n"),
			wantErr: "empty property name",
		},
		{
			name:    "component without matching END",
			invalid: []byte("BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:x\n"),
			wantErr: "has no matching END:VEVENT",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Split(valid, test.invalid)
			if err == nil {
				t.Fatalf("Split unexpectedly accepted %q", test.invalid)
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

func TestSplitMatchesEventsWithNestedComponents(t *testing.T) {
	alarm := "BEGIN:VALARM\nACTION:DISPLAY\nDESCRIPTION:Reminder\nEND:VALARM\n"
	shared := event("alarm@example.test", "A", "20261028T210000", alarm)
	got, err := Split(calendar(shared), calendar(shared))
	if err != nil {
		t.Fatalf("Split returned error: %v", err)
	}
	assertUIDs(t, eventUIDs(t, got.Shared), []string{"alarm@example.test"})
	assertUIDs(t, eventUIDs(t, got.FirstOnly), nil)
	assertUIDs(t, eventUIDs(t, got.SecondOnly), nil)
	if !strings.Contains(string(got.Shared), "ACTION:DISPLAY") {
		t.Error("shared output lost the nested VALARM component")
	}
}

func TestSplitAddsCalendarDefaults(t *testing.T) {
	bare := []byte("BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:nodefaults@example.test\r\nDTSTART:20261027T140000Z\r\nDTEND:20261027T153000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	got, err := Split(bare, bare)
	if err != nil {
		t.Fatalf("Split returned error: %v", err)
	}
	assertUIDs(t, eventUIDs(t, got.Shared), []string{"nodefaults@example.test"})
	for name, marker := range map[string]string{
		"PRODID":  "PRODID:-//ical-splitter//EN\r\n",
		"VERSION": "VERSION:2.0\r\n",
	} {
		if !strings.Contains(string(got.Shared), marker) {
			t.Errorf("shared output is missing the default %s property", name)
		}
	}
}

func TestSplitMatchesEventsWithoutDTStamp(t *testing.T) {
	shared := event("nodt@example.test", "No stamp", "", "")
	got, err := Split(calendar(shared), calendar(shared))
	if err != nil {
		t.Fatalf("Split returned error: %v", err)
	}
	assertUIDs(t, eventUIDs(t, got.Shared), []string{"nodt@example.test"})
}

func TestSplitMergesTimezonesWithoutTZID(t *testing.T) {
	tz := "BEGIN:VTIMEZONE\nX-LIC-LOCATION:Europe/Berlin\nEND:VTIMEZONE\n"
	first := calendar(tz, event("a@example.test", "A", "20261028T210000", ""))
	second := calendar(event("b@example.test", "B", "20261028T220000", ""))

	got, err := Split(first, second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got.FirstOnly), "X-LIC-LOCATION:Europe/Berlin") {
		t.Error("first-only output is missing the VTIMEZONE that has no TZID")
	}
}

func TestSplitMatchesEventsWithQuotedAndEscapedHeaderParts(t *testing.T) {
	tricky := event("tricky@example.test", "Tricky", "20261028T210000",
		`X-TRICKY;;CN="a;b";ROLE;NOTE="x\;y":tail`+"\n")
	got, err := Split(calendar(tricky), calendar(tricky))
	if err != nil {
		t.Fatalf("Split returned error: %v", err)
	}
	assertUIDs(t, eventUIDs(t, got.Shared), []string{"tricky@example.test"})
	if !strings.Contains(string(got.Shared), `NOTE="x\;y"`) {
		t.Error("shared output lost the tricky property's original line")
	}
}
