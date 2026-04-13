package areaapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"time"

	"copyright-crawler/internal/model"
)

var rng = rand.New(rand.NewSource(time.Now().UnixNano()))

type Client struct {
	HTTP             *http.Client
	Token            string
	AuthorizationKey string
	GatewayHeaders   map[string]string
	CookieHeader     string
	MinInterval      time.Duration
	MaxInterval      time.Duration
}

func (c *Client) FetchAllRaw(ctx context.Context) ([]byte, model.FailedReport, error) {
	token := strings.TrimSpace(c.Token)
	if token == "" {
		return nil, model.FailedReport{}, fmt.Errorf("缺少可用 token，无法请求地区接口")
	}

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 90 * time.Second}
	}

	// Gateway side is sensitive to burst traffic; mimic human pacing with jitter.
	minRequestInterval := c.MinInterval
	maxRequestInterval := c.MaxInterval
	if minRequestInterval == 0 {
		minRequestInterval = 900 * time.Millisecond
	}
	if maxRequestInterval == 0 {
		maxRequestInterval = 1800 * time.Millisecond
	}

	failed := model.FailedReport{
		FailedProvinces: make([]model.FailedProvince, 0),
		FailedCities:    make([]model.FailedCity, 0),
	}

	provinces, err := c.fetchAreaNodes(ctx, httpClient, "https://gateway.ccopyright.com.cn/userServer/area/province/001")
	if err != nil {
		// Still return a valid JSON array so caller can write output.json.
		return []byte("[]"), failed, fmt.Errorf("获取省份失败: %w", err)
	}
	sleepHuman(ctx, minRequestInterval, maxRequestInterval)

	var partialErr error
	raw := make([]model.RawProvinceLoc, 0, len(provinces))
	for _, p := range provinces {
		provinceURL := "https://gateway.ccopyright.com.cn/userServer/area/city/" + p.ID + "/1"
		prov := model.RawProvinceLoc{
			Province:     p.Name,
			ProvinceCode: p.ID,
			Cities:       make([]model.RawCityLoc, 0),
		}

		if p.HasChildren == 1 {
			cities, err := c.fetchAreaNodes(ctx, httpClient, provinceURL)
			if err != nil {
				// Best-effort: don't abort the whole crawl if one province's cities fail.
				// Keep output structure consistent by including the province with empty cities.
				failed.FailedProvinces = append(failed.FailedProvinces, model.FailedProvince{
					Province:     p.Name,
					ProvinceCode: p.ID,
					URL:          provinceURL,
					Error:        err.Error(),
				})
				if partialErr == nil {
					partialErr = fmt.Errorf("获取省份 %s(%s) 下城市失败: %w", p.Name, p.ID, err)
				}
				raw = append(raw, prov)
				continue
			}
			sleepHuman(ctx, minRequestInterval, maxRequestInterval)

			prov.Cities = make([]model.RawCityLoc, 0, len(cities))
			for _, cityNode := range cities {
				city := model.RawCityLoc{
					City:      cityNode.Name,
					CityCode:  cityNode.ID,
					Districts: make([]model.RawDistrictLoc, 0),
				}

				// Direct-controlled municipalities have only province+city in this dataset (no district layer).
				if isMunicipality(p.Name) {
					prov.Cities = append(prov.Cities, city)
					continue
				}

				// NOTE: area API's "hasChildren" on city nodes is not reliable in practice (often 0),
				// but districts are still queryable; always attempt district fetch.
				districts, usedURL, err := c.fetchDistrictNodes(ctx, httpClient, cityNode.ID)
				if err != nil {
					// Best-effort: a subset of cities intermittently fails with 502; don't abort the whole crawl.
					// Keep Districts empty for this city and continue.
					failed.FailedCities = append(failed.FailedCities, model.FailedCity{
						Province:     p.Name,
						ProvinceCode: p.ID,
						City:         cityNode.Name,
						CityCode:     cityNode.ID,
						URL:          usedURL,
						Error:        err.Error(),
					})
					if partialErr == nil {
						partialErr = fmt.Errorf("获取城市 %s(%s) 下区县失败: %w", cityNode.Name, cityNode.ID, err)
					}
					prov.Cities = append(prov.Cities, city)
					continue
				}
				sleepHuman(ctx, minRequestInterval, maxRequestInterval)

				if len(districts) == 0 {
					// Treat "empty districts" as a failed item for later retry/backfill.
					failed.FailedCities = append(failed.FailedCities, model.FailedCity{
						Province:     p.Name,
						ProvinceCode: p.ID,
						City:         cityNode.Name,
						CityCode:     cityNode.ID,
						URL:          usedURL,
						Error:        "empty districts",
					})
				}

				city.Districts = make([]model.RawDistrictLoc, 0, len(districts))
				for _, d := range districts {
					city.Districts = append(city.Districts, model.RawDistrictLoc{
						District:     d.Name,
						DistrictCode: d.ID,
					})
				}
				prov.Cities = append(prov.Cities, city)
			}
		}

		raw = append(raw, prov)
	}

	body, err := json.Marshal(raw)
	if err != nil {
		return nil, failed, fmt.Errorf("序列化原始 JSON 失败: %w", err)
	}
	return body, failed, partialErr
}

func (c *Client) RetryFromFailed(ctx context.Context, fr model.FailedReport) ([]model.ProvinceLoc, model.FailedReport, error) {
	token := strings.TrimSpace(c.Token)
	if token == "" {
		return nil, model.FailedReport{}, fmt.Errorf("缺少可用 token，无法重试地区接口")
	}

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 90 * time.Second}
	}

	minRequestInterval := c.MinInterval
	maxRequestInterval := c.MaxInterval
	if minRequestInterval == 0 {
		minRequestInterval = 900 * time.Millisecond
	}
	if maxRequestInterval == 0 {
		maxRequestInterval = 1800 * time.Millisecond
	}

	retryFailed := model.FailedReport{
		FailedProvinces: make([]model.FailedProvince, 0),
		FailedCities:    make([]model.FailedCity, 0),
	}

	// Merge into a single "subset" output.
	provByCode := make(map[string]*model.ProvinceLoc)
	cityByProv := make(map[string]map[string]*model.CityLoc) // provCode -> cityCode -> *CityLoc

	getOrCreateProvince := func(provName, provCode string) *model.ProvinceLoc {
		if provCode == "" {
			// Fallback to name key, but prefer codes when present.
			provCode = provName
		}
		if p, ok := provByCode[provCode]; ok {
			if p.Province == "" && provName != "" {
				p.Province = provName
			}
			if p.ProvinceCode == "" && provCode != "" {
				p.ProvinceCode = provCode
			}
			return p
		}
		p := &model.ProvinceLoc{
			Province:     provName,
			ProvinceCode: provCode,
			Cities:       make([]*model.CityLoc, 0),
		}
		provByCode[provCode] = p
		cityByProv[provCode] = make(map[string]*model.CityLoc)
		return p
	}

	addOrMergeCity := func(prov *model.ProvinceLoc, city *model.CityLoc) {
		if prov == nil || city == nil {
			return
		}
		provCode := prov.ProvinceCode
		if provCode == "" {
			provCode = prov.Province
		}
		if _, ok := cityByProv[provCode]; !ok {
			cityByProv[provCode] = make(map[string]*model.CityLoc)
		}
		cKey := city.CityCode
		if cKey == "" {
			cKey = city.City
		}
		if existing, ok := cityByProv[provCode][cKey]; ok {
			// Prefer non-empty districts over empty ones.
			if len(existing.Districts) == 0 && len(city.Districts) > 0 {
				existing.Districts = city.Districts
			}
			if existing.City == "" && city.City != "" {
				existing.City = city.City
			}
			if existing.CityCode == "" && city.CityCode != "" {
				existing.CityCode = city.CityCode
			}
			return
		}
		prov.Cities = append(prov.Cities, city)
		cityByProv[provCode][cKey] = city
	}

	// Track provinces where city list fetch succeeded, so we can skip duplicate per-city retries.
	provinceCitiesFetched := make(map[string]bool)

	// 1) Retry failed provinces: fetch their city list, then districts.
	for _, fp := range fr.FailedProvinces {
		provCode := strings.TrimSpace(fp.ProvinceCode)
		provName := strings.TrimSpace(fp.Province)
		prov := getOrCreateProvince(provName, provCode)

		if provCode == "" {
			retryFailed.FailedProvinces = append(retryFailed.FailedProvinces, model.FailedProvince{
				Province:     provName,
				ProvinceCode: provCode,
				URL:          "",
				Error:        "missing ProvinceCode",
			})
			continue
		}

		provinceURL := "https://gateway.ccopyright.com.cn/userServer/area/city/" + provCode + "/1"
		cities, err := c.fetchAreaNodes(ctx, httpClient, provinceURL)
		if err != nil {
			retryFailed.FailedProvinces = append(retryFailed.FailedProvinces, model.FailedProvince{
				Province:     provName,
				ProvinceCode: provCode,
				URL:          provinceURL,
				Error:        err.Error(),
			})
			continue
		}
		provinceCitiesFetched[provCode] = true
		sleepHuman(ctx, minRequestInterval, maxRequestInterval)

		for _, cityNode := range cities {
			city := &model.CityLoc{
				City:      cityNode.Name,
				CityCode:  cityNode.ID,
				Districts: make([]*model.DistrictLoc, 0),
			}

			if isMunicipality(provName) {
				// For municipalities, we keep Districts empty.
				addOrMergeCity(prov, city)
				continue
			}

			districts, usedURL, err := c.fetchDistrictNodes(ctx, httpClient, cityNode.ID)
			if err != nil {
				retryFailed.FailedCities = append(retryFailed.FailedCities, model.FailedCity{
					Province:     provName,
					ProvinceCode: provCode,
					City:         cityNode.Name,
					CityCode:     cityNode.ID,
					URL:          usedURL,
					Error:        err.Error(),
				})
				continue
			}
			sleepHuman(ctx, minRequestInterval, maxRequestInterval)

			if len(districts) == 0 {
				retryFailed.FailedCities = append(retryFailed.FailedCities, model.FailedCity{
					Province:     provName,
					ProvinceCode: provCode,
					City:         cityNode.Name,
					CityCode:     cityNode.ID,
					URL:          usedURL,
					Error:        "empty districts",
				})
				continue
			}

			city.Districts = make([]*model.DistrictLoc, 0, len(districts))
			for _, d := range districts {
				city.Districts = append(city.Districts, &model.DistrictLoc{
					District:     d.Name,
					DistrictCode: d.ID,
				})
			}
			addOrMergeCity(prov, city)
		}
	}

	// 2) Retry failed cities: fetch their district list only.
	for _, fc := range fr.FailedCities {
		provCode := strings.TrimSpace(fc.ProvinceCode)
		provName := strings.TrimSpace(fc.Province)
		if provinceCitiesFetched[provCode] {
			// Province retry already attempted all cities (best-effort), avoid extra duplicate calls.
			continue
		}
		if isMunicipality(provName) {
			// No district layer to backfill for municipalities.
			continue
		}

		prov := getOrCreateProvince(provName, provCode)

		cityCode := strings.TrimSpace(fc.CityCode)
		cityName := strings.TrimSpace(fc.City)
		if cityCode == "" {
			retryFailed.FailedCities = append(retryFailed.FailedCities, model.FailedCity{
				Province:     provName,
				ProvinceCode: provCode,
				City:         cityName,
				CityCode:     cityCode,
				URL:          "",
				Error:        "missing CityCode",
			})
			continue
		}
		districts, usedURL, err := c.fetchDistrictNodes(ctx, httpClient, cityCode)
		if err != nil {
			retryFailed.FailedCities = append(retryFailed.FailedCities, model.FailedCity{
				Province:     provName,
				ProvinceCode: provCode,
				City:         cityName,
				CityCode:     cityCode,
				URL:          usedURL,
				Error:        err.Error(),
			})
			continue
		}
		sleepHuman(ctx, minRequestInterval, maxRequestInterval)

		if len(districts) == 0 {
			retryFailed.FailedCities = append(retryFailed.FailedCities, model.FailedCity{
				Province:     provName,
				ProvinceCode: provCode,
				City:         cityName,
				CityCode:     cityCode,
				URL:          usedURL,
				Error:        "empty districts",
			})
			continue
		}

		city := &model.CityLoc{
			City:      cityName,
			CityCode:  cityCode,
			Districts: make([]*model.DistrictLoc, 0, len(districts)),
		}
		for _, d := range districts {
			city.Districts = append(city.Districts, &model.DistrictLoc{
				District:     d.Name,
				DistrictCode: d.ID,
			})
		}
		addOrMergeCity(prov, city)
	}

	// Finalize: convert map to slice and sort for stable output.
	out := make([]model.ProvinceLoc, 0, len(provByCode))
	for _, p := range provByCode {
		if p == nil || len(p.Cities) == 0 {
			continue
		}
		sort.Slice(p.Cities, func(i, j int) bool {
			ci, cj := p.Cities[i], p.Cities[j]
			ki, kj := "", ""
			if ci != nil {
				ki = ci.CityCode
				if ki == "" {
					ki = ci.City
				}
			}
			if cj != nil {
				kj = cj.CityCode
				if kj == "" {
					kj = cj.City
				}
			}
			return ki < kj
		})
		for _, c := range p.Cities {
			if c == nil || len(c.Districts) == 0 {
				continue
			}
			sort.Slice(c.Districts, func(i, j int) bool {
				di, dj := c.Districts[i], c.Districts[j]
				ki, kj := "", ""
				if di != nil {
					ki = di.DistrictCode
					if ki == "" {
						ki = di.District
					}
				}
				if dj != nil {
					kj = dj.DistrictCode
					if kj == "" {
						kj = dj.District
					}
				}
				return ki < kj
			})
		}
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		pi, pj := out[i], out[j]
		ki, kj := pi.ProvinceCode, pj.ProvinceCode
		if ki == "" {
			ki = pi.Province
		}
		if kj == "" {
			kj = pj.Province
		}
		return ki < kj
	})

	return out, retryFailed, nil
}

func isMunicipality(provinceName string) bool {
	switch strings.TrimSpace(provinceName) {
	case "北京", "北京市", "上海", "上海市", "天津", "天津市", "重庆", "重庆市":
		return true
	default:
		return false
	}
}

func (c *Client) fetchDistrictNodes(ctx context.Context, httpClient *http.Client, cityID string) ([]model.AreaNode, string, error) {
	// The last path segment is an API "level" parameter which varies across deployments.
	// Empirically, districts are often under ".../area/{cityID}/1" (not ".../city/{cityID}/...").
	// Cities are under ".../city/{provinceID}/1".
	base := "https://gateway.ccopyright.com.cn/userServer/area/area/" + cityID + "/"

	// Try level=2 first, then fall back to level=1.
	url2 := base + "2"
	nodes, err := c.fetchAreaNodes(ctx, httpClient, url2)
	if err == nil && len(nodes) > 0 {
		return nodes, url2, nil
	}

	// If level=2 yields empty or error, try level=1.
	url1 := base + "1"
	nodes1, err1 := c.fetchAreaNodes(ctx, httpClient, url1)
	if err1 == nil {
		return nodes1, url1, nil
	}

	// Prefer the fallback error if it fails too; otherwise surface the original.
	return nil, url1, err1
}

func (c *Client) fetchAreaNodes(ctx context.Context, httpClient *http.Client, url string) ([]model.AreaNode, error) {
	// Be conservative: longer retry window with exponential backoff + jitter to look less like a bot.
	const maxAttempts = 8
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}

		// Copy gateway headers (from browser) but filter out invalid/unsafe ones.
		for key, value := range c.GatewayHeaders {
			if value == "" {
				continue
			}
			switch strings.ToLower(key) {
			case "host", "content-length":
				continue
			}
			req.Header.Set(key, value)
		}

		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("token", c.Token)
		req.Header.Set("Authorization", c.Token)

		authorizationKey := strings.TrimSpace(c.AuthorizationKey)
		if authorizationKey == "" {
			authorizationKey = strings.TrimSpace(c.GatewayHeaders["authorization_key"])
		}
		if authorizationKey == "" {
			authorizationKey = strings.TrimSpace(c.GatewayHeaders["Authorization_Key"])
		}
		if authorizationKey != "" {
			req.Header.Set("authorization_key", authorizationKey)
		}
		if c.CookieHeader != "" && req.Header.Get("Cookie") == "" {
			req.Header.Set("Cookie", c.CookieHeader)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt == maxAttempts || !shouldRetryAreaRequest(0, err) {
				return nil, err
			}
			waitBeforeRetry(ctx, attempt, nil)
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt == maxAttempts || !shouldRetryAreaRequest(0, readErr) {
				return nil, readErr
			}
			waitBeforeRetry(ctx, attempt, resp)
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			preview := string(body)
			if len(preview) > 300 {
				preview = preview[:300] + "..."
			}
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, preview)
			if attempt == maxAttempts || !shouldRetryAreaRequest(resp.StatusCode, nil) {
				return nil, lastErr
			}
			fmt.Printf("地区接口请求失败，准备重试(%d/%d): %s\n", attempt, maxAttempts, url)
			waitBeforeRetry(ctx, attempt, resp)
			continue
		}

		var result model.AreaAPIResponse
		if err := json.Unmarshal(body, &result); err != nil {
			preview := string(body)
			if len(preview) > 300 {
				preview = preview[:300] + "..."
			}
			return nil, fmt.Errorf("接口响应解析失败: %w; 片段=%q", err, preview)
		}

		if isAreaAPIError(result) {
			return nil, fmt.Errorf("接口返回异常 code=%d returnCode=%s msg=%s message=%s", result.Code, result.ReturnCode, result.Msg, result.Message)
		}

		return result.Data, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("地区接口请求失败: %s", url)
}

func shouldRetryAreaRequest(statusCode int, err error) bool {
	if err != nil {
		return true
	}
	switch statusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func waitBeforeRetry(ctx context.Context, attempt int, resp *http.Response) {
	// Honor Retry-After when present (common with 429).
	if resp != nil {
		if ra := strings.TrimSpace(resp.Header.Get("Retry-After")); ra != "" {
			if secs, err := time.ParseDuration(ra + "s"); err == nil && secs > 0 {
				sleepWithContext(ctx, secs)
				return
			}
		}
	}

	// Exponential backoff with jitter, capped.
	// attempt=1 => ~2s, attempt=2 => ~4s, ...
	const base = 2 * time.Second
	const max = 60 * time.Second
	delay := base << (attempt - 1)
	if delay > max {
		delay = max
	}
	// Add 0-1200ms jitter.
	delay += time.Duration(rng.Int63n(int64(1200 * time.Millisecond)))
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func sleepHuman(ctx context.Context, min, max time.Duration) {
	if min <= 0 && max <= 0 {
		return
	}
	if min <= 0 {
		min = max
	}
	if max < min {
		max = min
	}
	span := max - min
	if span <= 0 {
		sleepWithContext(ctx, min)
		return
	}
	d := min + time.Duration(rng.Int63n(int64(span)+1))
	sleepWithContext(ctx, d)
}

func isAreaAPIError(result model.AreaAPIResponse) bool {
	if strings.EqualFold(result.ReturnCode, "FAILED") {
		return true
	}
	if result.ReturnCode != "" && !strings.EqualFold(result.ReturnCode, "SUCCESS") {
		return true
	}
	msg := strings.ToLower(result.Msg + " " + result.Message)
	if strings.Contains(msg, "failed") || strings.Contains(msg, "error") {
		return true
	}
	if len(result.Data) == 0 && strings.EqualFold(strings.TrimSpace(result.Msg), "Operate failed") {
		return true
	}
	return false
}
