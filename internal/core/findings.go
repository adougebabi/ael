package core

import (
	"ael/internal/store"
	"encoding/json"
	"path"
	"slices"
	"strings"
)

type FindingLocation struct {
	Path     string `json:"path,omitempty"`
	Selector string `json:"selector,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
}

type FindingInput struct {
	Rule           string           `json:"rule"`
	Title          string           `json:"title"`
	Kind           string           `json:"kind"`
	Severity       string           `json:"severity"`
	Route          string           `json:"route"`
	Location       FindingLocation  `json:"location"`
	Expected       string           `json:"expected"`
	Actual         string           `json:"actual"`
	Reproduction   []string         `json:"reproduction"`
	Impact         string           `json:"impact"`
	Artifacts      []string         `json:"artifacts"`
	TraceArtifacts []string         `json:"trace_artifacts,omitempty"`
	Environment    string           `json:"environment,omitempty"`
	Viewport       *FindingViewport `json:"viewport,omitempty"`
}

type FindingViewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}
type FindingReportSource struct {
	Method  string `json:"method"`
	Tool    string `json:"tool,omitempty"`
	Version string `json:"version,omitempty"`
	RunID   string `json:"run_id,omitempty"`
}

type FindingSource struct {
	FindingReportSource
	Actor       string `json:"actor"`
	Attestation string `json:"attestation"`
}

type FindingEvidence struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MIME     string `json:"mime"`
	SHA256   string `json:"sha256"`
	Role     string `json:"role"`
	Status   string `json:"status"`
	Uploader string `json:"uploader"`
}

type Finding struct {
	ID                 string             `json:"id"`
	TaskID             string             `json:"task_id"`
	CheckID            string             `json:"check_id"`
	Candidate          string             `json:"candidate_id"`
	DefinitionRevision int                `json:"definition_revision"`
	PlanRevision       int                `json:"plan_revision"`
	Config             string             `json:"config_sha256"`
	Fingerprint        string             `json:"fingerprint"`
	Rule               string             `json:"rule"`
	Title              string             `json:"title"`
	Kind               string             `json:"kind"`
	Severity           string             `json:"severity"`
	Route              string             `json:"route"`
	Location           FindingLocation    `json:"location"`
	Status             string             `json:"status"`
	FirstSeen          string             `json:"first_seen"`
	LastSeen           string             `json:"last_seen"`
	Decision           *FindingDecision   `json:"decision,omitempty"`
	Repair             *FindingRepair     `json:"repair,omitempty"`
	Resolution         *FindingResolution `json:"resolution,omitempty"`
	RecurrenceOf       string             `json:"recurrence_of,omitempty"`
	LineageID          string             `json:"lineage_id,omitempty"`
}

type FindingOccurrence struct {
	ID               string            `json:"id"`
	FindingID        string            `json:"finding_id"`
	CheckReportID    string            `json:"check_report_id"`
	ContextID        string            `json:"context_id"`
	ReportArtifactID string            `json:"report_artifact_id,omitempty"`
	ReportSHA256     string            `json:"report_sha256,omitempty"`
	TaskRevision     string            `json:"task_revision"`
	Input            FindingInput      `json:"input"`
	Source           FindingSource     `json:"source"`
	Evidence         []FindingEvidence `json:"evidence"`
	Completeness     string            `json:"completeness"`
	Missing          []string          `json:"missing_evidence"`
	Created          string            `json:"created_at"`
}

// NormalizeFinding identifies an issue by version, rule and exact location.
// Titles and observations remain in each immutable occurrence.
func NormalizeFinding(input FindingInput, checkKind string) (FindingInput, error) {
	input.Rule, input.Title, input.Route = strings.TrimSpace(input.Rule), strings.TrimSpace(input.Title), strings.TrimSpace(input.Route)
	input.Location.Path = strings.TrimSpace(input.Location.Path)
	input.Location.Selector = strings.TrimSpace(input.Location.Selector)
	if input.Location.Path != "" {
		if strings.ContainsAny(input.Location.Path, "\\\x00\r\n") || path.IsAbs(input.Location.Path) || input.Location.Path == ".." || strings.HasPrefix(input.Location.Path, "../") {
			return input, Error("FINDING_INVALID", "code locations must be relative paths")
		}
		input.Location.Path = path.Clean(input.Location.Path)
		if input.Location.Path == ".." || strings.HasPrefix(input.Location.Path, "../") {
			return input, Error("FINDING_INVALID", "code location escapes the project")
		}
	}
	if input.Kind == "" {
		input.Kind = "functional"
	}
	if checkKind == "ui" || checkKind == "ux" {
		input.Kind = checkKind
	}
	if input.Severity == "" {
		input.Severity = "unknown"
	}
	if input.Rule == "" || input.Title == "" || (input.Route == "" && input.Location.Path == "" && input.Location.Selector == "") || input.Location.Line < 0 || input.Location.Column < 0 || !slices.Contains([]string{"ui", "ux", "functional"}, input.Kind) || !slices.Contains([]string{"unknown", "info", "low", "medium", "high", "critical"}, input.Severity) {
		return input, Error("FINDING_INVALID", "rule, title, stable location and valid classification are required")
	}
	for _, field := range []struct {
		value string
		limit int
	}{{input.Rule, 200}, {input.Title, 400}, {input.Route, 2000}, {input.Location.Path, 1000}, {input.Location.Selector, 2000}, {input.Expected, 6000}, {input.Actual, 6000}, {input.Impact, 4000}, {input.Environment, 2000}} {
		if len(field.value) > field.limit || strings.ContainsRune(field.value, '\x00') {
			return input, Error("FINDING_INVALID", "finding field exceeds its limit")
		}
	}
	if len(input.Reproduction) > 50 || len(input.Artifacts) > 32 || len(input.TraceArtifacts) > 32 {
		return input, Error("FINDING_INVALID", "too many steps or evidence items")
	}
	for _, step := range input.Reproduction {
		if strings.TrimSpace(step) == "" || len(step) > 2000 {
			return input, Error("FINDING_INVALID", "reproduction steps must contain concise observations")
		}
	}
	if input.Viewport != nil && (input.Viewport.Width <= 0 || input.Viewport.Height <= 0 || input.Viewport.Width > 32768 || input.Viewport.Height > 32768) {
		return input, Error("FINDING_INVALID", "invalid reported viewport")
	}
	return input, nil
}

func FindingFingerprint(task, check, candidate, config string, revision, plan int, input FindingInput) string {
	canonical, _ := json.Marshal(struct {
		Task, Check, Candidate, Config, Rule, Route, Kind string
		Revision, Plan                                    int
		Location                                          FindingLocation
	}{task, check, candidate, config, input.Rule, input.Route, input.Kind, revision, plan, input.Location})
	return store.Hash(canonical)
}

func FindingMissing(input FindingInput, report string, evidence []FindingEvidence) []string {
	missing := []string{}
	if report == "" {
		missing = append(missing, "report")
	}
	if strings.TrimSpace(input.Expected) == "" {
		missing = append(missing, "expected")
	}
	if strings.TrimSpace(input.Actual) == "" {
		missing = append(missing, "actual")
	}
	if len(input.Reproduction) == 0 {
		missing = append(missing, "reproduction")
	}
	if strings.TrimSpace(input.Impact) == "" {
		missing = append(missing, "impact")
	}
	image, interaction := false, false
	for _, item := range evidence {
		if item.Status != "finalized" {
			missing = append(missing, "unavailable:"+item.ID)
			continue
		}
		if item.MIME == "image/png" || item.MIME == "image/jpeg" {
			image = true
		}
		if slices.Contains(input.TraceArtifacts, item.ID) && (item.MIME == "text/plain" || item.MIME == "video/mp4" || item.MIME == "application/octet-stream") {
			interaction = true
		}
	}
	if input.Kind == "ui" && !image {
		missing = append(missing, "screenshot")
	}
	if input.Kind == "ux" && !interaction {
		missing = append(missing, "interaction_trace")
	}
	return missing
}
