package main

import (
	"testing"

	"github.com/synapctx/sctx/internal/application/report"
)

func TestParseGainArgsViews(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		check   func(t *testing.T, o report.Options)
	}{
		{"daily", []string{"--daily"}, false, func(t *testing.T, o report.Options) {
			if o.Period != report.PeriodDaily {
				t.Errorf("Period = %q", o.Period)
			}
		}},
		{"weekly csv", []string{"--weekly", "--format", "csv"}, false, func(t *testing.T, o report.Options) {
			if o.Period != report.PeriodWeekly || o.Format != "csv" {
				t.Errorf("got %+v", o)
			}
		}},
		{"monthly markdown", []string{"--monthly", "--format", "markdown"}, false, nil},
		{"graph", []string{"--graph"}, false, func(t *testing.T, o report.Options) {
			if !o.Graph {
				t.Error("Graph not set")
			}
		}},
		{"history default", []string{"--history"}, false, func(t *testing.T, o report.Options) {
			if !o.History || o.Limit != 0 {
				t.Errorf("got %+v", o)
			}
		}},
		{"history N then flag", []string{"--history", "5", "--format", "json"}, false, func(t *testing.T, o report.Options) {
			if o.Limit != 5 || o.Format != "json" {
				t.Errorf("got %+v", o)
			}
		}},
		{"history before flag", []string{"--history", "--format", "csv"}, false, func(t *testing.T, o report.Options) {
			if o.Limit != 0 || o.Format != "csv" {
				t.Errorf("got %+v", o)
			}
		}},
		{"history zero", []string{"--history", "0"}, true, nil},
		{"summary csv", []string{"--format", "csv"}, false, nil},
		{"two periods", []string{"--daily", "--weekly"}, true, nil},
		{"period and graph", []string{"--daily", "--graph"}, true, nil},
		{"view with share", []string{"--graph", "--share"}, true, nil},
		{"view with failures", []string{"--history", "--failures"}, true, nil},
		{"csv with share", []string{"--share", "--format", "csv"}, true, nil},
		{"csv with failures", []string{"--failures", "--format", "csv"}, true, nil},
		{"summary markdown still rejected", []string{"--format", "markdown"}, true, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o, err := parseGainArgs(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.check != nil && err == nil {
				tc.check(t, o)
			}
		})
	}
}
