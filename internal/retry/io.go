package retry

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"copyright-crawler/internal/model"
)

// LoadFailedReport loads a failed.json report from disk.
func LoadFailedReport(path string) (model.FailedReport, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return model.FailedReport{}, fmt.Errorf("failed.json 路径为空")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return model.FailedReport{}, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return model.FailedReport{}, fmt.Errorf("failed.json 内容为空: %s", path)
	}
	var fr model.FailedReport
	if err := json.Unmarshal(b, &fr); err != nil {
		return model.FailedReport{}, err
	}
	// Treat empty lists as valid; caller may still want deterministic outputs.
	if fr.FailedProvinces == nil {
		fr.FailedProvinces = make([]model.FailedProvince, 0)
	}
	if fr.FailedCities == nil {
		fr.FailedCities = make([]model.FailedCity, 0)
	}
	return fr, nil
}

