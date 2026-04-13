package model

// ProvinceLoc 省份层级
type ProvinceLoc struct {
	Province     string     `json:"Province"`
	ProvinceCode string     `json:"ProvinceCode"`
	Cities       []*CityLoc `json:"Cities"`
}

// CityLoc 城市层级
type CityLoc struct {
	City      string         `json:"City"`
	CityCode  string         `json:"CityCode"`
	Districts []*DistrictLoc `json:"Districts"`
}

// DistrictLoc 区县层级
type DistrictLoc struct {
	District     string `json:"District"`
	DistrictCode string `json:"DistrictCode"`
}

// RawProvinceLoc matches the upstream raw JSON schema.
type RawProvinceLoc struct {
	Province     string       `json:"Province"`
	ProvinceCode string       `json:"ProvinceCode"`
	Cities       []RawCityLoc `json:"Cities"`
}

// RawCityLoc matches the upstream raw JSON schema.
type RawCityLoc struct {
	City      string           `json:"City"`
	CityCode  string           `json:"CityCode"`
	Districts []RawDistrictLoc `json:"Districts"`
}

// RawDistrictLoc matches the upstream raw JSON schema.
type RawDistrictLoc struct {
	District     string `json:"District"`
	DistrictCode string `json:"DistrictCode"`
}

// AreaAPIResponse is the gateway "area" API response envelope.
type AreaAPIResponse struct {
	Code       int        `json:"code"`
	Msg        string     `json:"msg"`
	Message    string     `json:"message"`
	ReturnCode string     `json:"returnCode"`
	Data       []AreaNode `json:"data"`
}

type AreaNode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	HasChildren int    `json:"hasChildren"`
}

type FailedProvince struct {
	Province     string `json:"Province"`
	ProvinceCode string `json:"ProvinceCode"`
	URL          string `json:"URL"`
	Error        string `json:"Error"`
}

type FailedCity struct {
	Province     string `json:"Province"`
	ProvinceCode string `json:"ProvinceCode"`
	City         string `json:"City"`
	CityCode     string `json:"CityCode"`
	URL          string `json:"URL"`
	Error        string `json:"Error"`
}

type FailedReport struct {
	FailedProvinces []FailedProvince `json:"FailedProvinces"`
	FailedCities    []FailedCity     `json:"FailedCities"`
}

