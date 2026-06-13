package cdp

import (
	"testing"
	"time"
)

func TestFindGranolaTarget(t *testing.T) {
	extractor := &CDPExtractor{}

	t.Run("finds page target", func(t *testing.T) {
		targets := []CDPTarget{
			{ID: "1", Type: "iframe", Title: "frame"},
			{ID: "2", Type: "page", Title: "Granola"},
			{ID: "3", Type: "other", Title: "background"},
		}
		target := extractor.findGranolaTarget(targets)
		if target == nil {
			t.Fatal("findGranolaTarget() returned nil")
		}
		if target.ID != "2" {
			t.Errorf("target ID = %q, want %q", target.ID, "2")
		}
	})

	t.Run("no page target", func(t *testing.T) {
		targets := []CDPTarget{
			{ID: "1", Type: "iframe"},
			{ID: "2", Type: "background"},
		}
		target := extractor.findGranolaTarget(targets)
		if target != nil {
			t.Errorf("findGranolaTarget() = %v, want nil", target)
		}
	})

	t.Run("empty targets", func(t *testing.T) {
		target := extractor.findGranolaTarget(nil)
		if target != nil {
			t.Errorf("findGranolaTarget() = %v, want nil", target)
		}
	})
}

func TestParseDateStr(t *testing.T) {
	now := time.Now()

	t.Run("empty string returns now", func(t *testing.T) {
		got := parseDateStr("")
		if got.IsZero() {
			t.Error("parseDateStr('') returned zero time")
		}
	})

	t.Run("today", func(t *testing.T) {
		got := parseDateStr("today")
		if got.Day() != now.Day() {
			t.Errorf("parseDateStr('today') = %v, want today", got)
		}
	})

	t.Run("yesterday", func(t *testing.T) {
		got := parseDateStr("yesterday")
		want := now.AddDate(0, 0, -1)
		if got.Day() != want.Day() {
			t.Errorf("parseDateStr('yesterday') = %v, want %v", got, want)
		}
	})

	t.Run("weekday name", func(t *testing.T) {
		got := parseDateStr("monday")
		if got.IsZero() {
			t.Error("parseDateStr('monday') returned zero time")
		}
		if got.Weekday() != time.Monday {
			t.Errorf("parseDateStr('monday') = %v, want Monday", got)
		}
	})

	t.Run("formatted date with year", func(t *testing.T) {
		got := parseDateStr("Jan 15, 2026")
		if got.Year() != 2026 || got.Month() != time.January || got.Day() != 15 {
			t.Errorf("parseDateStr('Jan 15, 2026') = %v", got)
		}
	})

	t.Run("formatted date without year", func(t *testing.T) {
		got := parseDateStr("Jan 15")
		if got.Month() != time.January || got.Day() != 15 {
			t.Errorf("parseDateStr('Jan 15') = %v", got)
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		got := parseDateStr("Today")
		if got.Day() != now.Day() {
			t.Errorf("parseDateStr('Today') = %v, want today", got)
		}
	})

	t.Run("trimmed whitespace", func(t *testing.T) {
		got := parseDateStr("  yesterday  ")
		want := now.AddDate(0, 0, -1)
		if got.Day() != want.Day() {
			t.Errorf("parseDateStr('  yesterday  ') = %v, want %v", got, want)
		}
	})
}
