package desktop

import (
	"math/big"
	"regexp"
	"strings"

	"silverwing-go/internal/application"
)

var sidebarEstimateDecimal = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

// Refresh state does not invalidate the last complete total for the same
// account, project configuration and Sprint. Incomplete new scopes stay unknown.
func (s *app) requirementsEstimateBadge() string {
	identity := s.currentRequirementListIdentity()
	state := s.requirementListJobs
	if s.requirementsSelectedSprintKey == "" || state != nil && (!state.hasIdentity || state.identity != identity) {
		return "—"
	}
	if s.hasCompleteRequirementsEstimate() {
		return requirementsEstimateTotal(s.requirements)
	}
	if state != nil {
		if cached, ok := state.snapshots[requirementListSnapshotKey{identity: identity, sprint: s.requirementsSelectedSprintKey}]; ok && cached.estimate != "" {
			return cached.estimate
		}
	}
	return "—"
}

func (s *app) hasCompleteRequirementsEstimate() bool {
	return s.requirementsLoaded && !s.requirementsLoading && s.requirementsLoadError == "" && len(s.requirementsFailures) == 0 && s.requirementsSelectedSprintKey != ""
}

// Rational arithmetic preserves the source decimal exactly; missing estimates
// remain unknown even when the enclosing list was read successfully.
func requirementsEstimateTotal(items []application.Requirement) string {
	total := new(big.Rat)
	for _, requirement := range items {
		raw := requirement.WorkItem.MySprintEstimateDays
		if raw == nil || !sidebarEstimateDecimal.MatchString(*raw) {
			return "—"
		}
		value, ok := new(big.Rat).SetString(*raw)
		if !ok {
			return "—"
		}
		total.Add(total, value)
	}
	denominator := new(big.Int).Set(total.Denom())
	twos := int(denominator.TrailingZeroBits())
	denominator.Rsh(denominator, uint(twos))
	fives := 0
	five, remainder, quotient := big.NewInt(5), new(big.Int), new(big.Int)
	for denominator.Cmp(big.NewInt(1)) > 0 {
		quotient.QuoRem(denominator, five, remainder)
		if remainder.Sign() != 0 {
			return "—"
		}
		denominator.Set(quotient)
		fives++
	}
	scale := max(twos, fives)
	text := total.FloatString(scale)
	if scale > 0 {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	return text
}
