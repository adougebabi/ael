package execution

import (
	"ael/internal/core"
	"bytes"
	"encoding/json"
	"io"
	"os"
)

// ReadAttemptResult requires an explicit, bounded JSON result; process output
// and a zero exit code alone never constitute a result.
func ReadAttemptResult(path string, review bool) (core.StepResult, core.Assessment, error) {
	var result core.StepResult
	var assessment core.Assessment
	f, err := os.Open(path)
	if err != nil {
		return result, assessment, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return result, assessment, core.Error("INVALID_RESULT", "result exceeds the size limit or is unreadable")
	}
	var target any = &result
	if review {
		target = &assessment
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return result, assessment, core.Error("INVALID_RESULT", "result JSON is invalid")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return result, assessment, core.Error("INVALID_RESULT", "trailing data")
	}
	return result, assessment, nil
}
