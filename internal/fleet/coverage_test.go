// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"slices"
	"testing"
)

func TestCoverageAssessment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   Coverage
		want CoverageAssessment
	}{
		{
			name: "fresh complete fleet",
			in:   Coverage{Requested: 2, Reachable: 2},
			want: CoverageAssessment{Complete: true},
		},
		{
			name: "empty requested set is complete evidence but not an authorization decision",
			in:   Coverage{},
			want: CoverageAssessment{Complete: true},
		},
		{
			name: "stale reachable scope is incomplete",
			in:   Coverage{Requested: 2, Reachable: 2, Stale: []string{"spoke-b"}},
			want: CoverageAssessment{
				Gaps:  []CoverageGap{CoverageGapStale},
				Stale: []string{"spoke-b"},
			},
		},
		{
			name: "truncated reachable scope is incomplete",
			in:   Coverage{Requested: 2, Reachable: 2, Truncated: []string{"spoke-b"}},
			want: CoverageAssessment{
				Gaps:      []CoverageGap{CoverageGapTruncated},
				Truncated: []string{"spoke-b"},
			},
		},
		{
			name: "unreachable scope is incomplete",
			in:   Coverage{Requested: 2, Reachable: 1, Unreachable: []string{"spoke-b"}},
			want: CoverageAssessment{
				Gaps:        []CoverageGap{CoverageGapUnreachable},
				Unreachable: []string{"spoke-b"},
			},
		},
		{
			name: "stale and unreachable scope is incomplete",
			in: Coverage{
				Requested: 2, Reachable: 1, Unreachable: []string{"spoke-b"}, Stale: []string{"spoke-b"},
			},
			want: CoverageAssessment{
				Gaps:        []CoverageGap{CoverageGapUnreachable, CoverageGapStale},
				Unreachable: []string{"spoke-b"},
				Stale:       []string{"spoke-b"},
			},
		},
		{
			name: "unaccounted requested scope is incomplete",
			in:   Coverage{Requested: 3, Reachable: 1, Unreachable: []string{"spoke-b"}},
			want: CoverageAssessment{
				Gaps:        []CoverageGap{CoverageGapUnreachable, CoverageGapUnaccounted},
				Unreachable: []string{"spoke-b"},
				Unaccounted: 1,
			},
		},
		{
			name: "contradictory counters are incomplete",
			in:   Coverage{Requested: 1, Reachable: 1, Unreachable: []string{"spoke-b"}},
			want: CoverageAssessment{
				Gaps:         []CoverageGap{CoverageGapInconsistent, CoverageGapUnreachable},
				Unreachable:  []string{"spoke-b"},
				Inconsistent: true,
			},
		},
		{
			name: "more stale names than requested scopes is inconsistent",
			in:   Coverage{Requested: 1, Reachable: 1, Stale: []string{"spoke-a", "spoke-b"}},
			want: CoverageAssessment{
				Gaps:         []CoverageGap{CoverageGapInconsistent, CoverageGapStale},
				Stale:        []string{"spoke-a", "spoke-b"},
				Inconsistent: true,
			},
		},
		{
			name: "more truncated names than reachable scopes is inconsistent",
			in:   Coverage{Requested: 2, Reachable: 1, Unreachable: []string{"spoke-b"}, Truncated: []string{"spoke-a", "spoke-c"}},
			want: CoverageAssessment{
				Gaps:         []CoverageGap{CoverageGapInconsistent, CoverageGapUnreachable, CoverageGapTruncated},
				Unreachable:  []string{"spoke-b"},
				Truncated:    []string{"spoke-a", "spoke-c"},
				Inconsistent: true,
			},
		},
		{
			name: "unreachable scope cannot also be truncated",
			in:   Coverage{Requested: 2, Reachable: 1, Unreachable: []string{"spoke-b"}, Truncated: []string{"spoke-b"}},
			want: CoverageAssessment{
				Gaps:         []CoverageGap{CoverageGapInconsistent, CoverageGapUnreachable, CoverageGapTruncated},
				Unreachable:  []string{"spoke-b"},
				Truncated:    []string{"spoke-b"},
				Inconsistent: true,
			},
		},
		{
			name: "duplicate and blank scope names are inconsistent",
			in:   Coverage{Requested: 2, Reachable: 0, Unreachable: []string{"spoke-b", "spoke-b"}, Stale: []string{" "}},
			want: CoverageAssessment{
				Gaps:         []CoverageGap{CoverageGapInconsistent, CoverageGapUnreachable, CoverageGapStale},
				Unreachable:  []string{"spoke-b", "spoke-b"},
				Stale:        []string{" "},
				Inconsistent: true,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := test.in.Assessment()
			if got.Complete != test.want.Complete || got.Unaccounted != test.want.Unaccounted || got.Inconsistent != test.want.Inconsistent ||
				!slices.Equal(got.Gaps, test.want.Gaps) || !slices.Equal(got.Unreachable, test.want.Unreachable) ||
				!slices.Equal(got.Stale, test.want.Stale) || !slices.Equal(got.Truncated, test.want.Truncated) {
				t.Fatalf("Assessment() = %#v, want %#v", got, test.want)
			}
			if complete := test.in.Complete(); complete != test.want.Complete {
				t.Fatalf("Complete() = %t, want %t", complete, test.want.Complete)
			}
		})
	}
}

func TestCoverageAssessmentDoesNotAliasInputSlices(t *testing.T) {
	t.Parallel()

	coverage := Coverage{
		Requested: 2, Reachable: 1, Unreachable: []string{"spoke-b"}, Stale: []string{"spoke-b"}, Truncated: []string{"spoke-a"},
	}
	assessment := coverage.Assessment()
	assessment.Unreachable[0] = "mutated"
	assessment.Stale[0] = "mutated"
	assessment.Truncated[0] = "mutated"

	if !slices.Equal(coverage.Unreachable, []string{"spoke-b"}) || !slices.Equal(coverage.Stale, []string{"spoke-b"}) ||
		!slices.Equal(coverage.Truncated, []string{"spoke-a"}) {
		t.Fatalf("Assessment() aliased Coverage input: %#v", coverage)
	}
}
