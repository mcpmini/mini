package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type checkStatus string

const (
	statusSuccess checkStatus = "SUCCESS"
	statusFailure checkStatus = "FAILURE"
	statusWarning checkStatus = "WARNING"
)

type check struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Status       checkStatus `json:"status"`
	Description  string      `json:"description"`
	ErrorMessage string      `json:"errorMessage"`
}

func (c check) explanation() string {
	if c.ErrorMessage != "" {
		return c.ErrorMessage
	}
	return c.Description
}

type outcome struct {
	Set      string
	Scenario string
	ExitCode int
	Ran      bool
	Problems []check
	Error    string
}

func (o outcome) passed() bool {
	return o.Error == "" && o.ExitCode == 0 && o.Ran && len(o.Problems) == 0
}

func anyFailed(outcomes []outcome) bool {
	for _, o := range outcomes {
		if !o.passed() {
			return true
		}
	}
	return false
}

// scenarioChecks returns every reason a scenario's checks.json fails it. The suite passes a scenario whose
// warnings or skipped checks leave nothing exercised, so the runner holds it to more.
func scenarioChecks(data []byte, scenario string) ([]check, error) {
	var checks []check
	if err := json.Unmarshal(data, &checks); err != nil {
		return nil, fmt.Errorf("decode checks.json: %w", err)
	}
	problems := failedChecks(checks)
	if !anySucceeded(checks) {
		problems = append(problems, check{
			ID: "scenario-exercised", Status: statusFailure, Description: "Scenario produced no successful checks",
		})
	}
	return append(problems, missingRequired(checks, requiredChecks[scenario])...), nil
}

func failedChecks(checks []check) []check {
	var failed []check
	for _, c := range checks {
		if c.Status == statusFailure || c.Status == statusWarning {
			failed = append(failed, c)
		}
	}
	return failed
}

func anySucceeded(checks []check) bool {
	for _, c := range checks {
		if c.Status == statusSuccess {
			return true
		}
	}
	return false
}

func missingRequired(checks []check, required []check) []check {
	var missing []check
	for _, want := range required {
		if !observedSuccess(checks, want) {
			missing = append(missing, check{
				ID: want.ID, Name: want.Name, Status: statusFailure,
				Description: "Required check did not succeed: " + want.ID + " " + want.Name,
			})
		}
	}
	return missing
}

func observedSuccess(checks []check, required check) bool {
	for _, c := range checks {
		if c.ID == required.ID && (required.Name == "" || c.Name == required.Name) && c.Status == statusSuccess {
			return true
		}
	}
	return false
}

// clientTimeoutError names the suite's client timeout, which would otherwise read as an adapter error in the summary.
func clientTimeoutError(output []byte) error {
	if strings.Contains(string(output), "Client timed out") {
		return errors.New("suite client timeout")
	}
	return nil
}
