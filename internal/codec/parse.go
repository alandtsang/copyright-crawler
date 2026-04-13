package codec

import (
	"encoding/json"
	"fmt"
	"strings"

	"copyright-crawler/internal/model"
)

// ParseRawProvinces converts the raw JSON (province -> city -> district) into output structure.
func ParseRawProvinces(body []byte) ([]model.ProvinceLoc, error) {
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return nil, fmt.Errorf("未获取到省市区原始 JSON")
	}

	var raw []model.RawProvinceLoc
	if err := json.Unmarshal(body, &raw); err != nil {
		preview := string(body)
		if len(preview) > 300 {
			preview = preview[:300] + "..."
		}
		return nil, fmt.Errorf("原始 JSON 解析失败: %w; 长度=%d; 片段=%q", err, len(body), preview)
	}

	result := make([]model.ProvinceLoc, 0, len(raw))
	for _, p := range raw {
		prov := model.ProvinceLoc{
			Province:     p.Province,
			ProvinceCode: p.ProvinceCode,
			Cities:       make([]*model.CityLoc, 0, len(p.Cities)),
		}

		for _, c := range p.Cities {
			city := &model.CityLoc{
				City:      c.City,
				CityCode:  c.CityCode,
				Districts: make([]*model.DistrictLoc, 0, len(c.Districts)),
			}

			for _, d := range c.Districts {
				city.Districts = append(city.Districts, &model.DistrictLoc{
					District:     d.District,
					DistrictCode: d.DistrictCode,
				})
			}

			prov.Cities = append(prov.Cities, city)
		}

		result = append(result, prov)
	}

	return result, nil
}

