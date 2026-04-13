package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

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

type rawProvinceLoc struct {
	Province     string       `json:"Province"`
	ProvinceCode string       `json:"ProvinceCode"`
	Cities       []rawCityLoc `json:"Cities"`
}

type rawCityLoc struct {
	City      string           `json:"City"`
	CityCode  string           `json:"CityCode"`
	Districts []rawDistrictLoc `json:"Districts"`
}

type rawDistrictLoc struct {
	District     string `json:"District"`
	DistrictCode string `json:"DistrictCode"`
}

type areaAPIResponse struct {
	Code       int        `json:"code"`
	Msg        string     `json:"msg"`
	Message    string     `json:"message"`
	ReturnCode string     `json:"returnCode"`
	Data       []areaNode `json:"data"`
}

type areaNode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	HasChildren int    `json:"hasChildren"`
}

func main() {
	// 1. 设置目标 URL 和凭证
	loginURL := "https://register.ccopyright.com.cn/login.html"
	username := "YOUR_USERNAME"
	password := "YOUR_PASSWORD"

	// 2. 初始化浏览器配置，设置为非无头模式以便调试查看，以及人工过验证码
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.WindowSize(1280, 800),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// 设置较长的超时时间，确保有足够时间完成一系列繁琐的点击操作和验证码
	ctx, cancel = context.WithTimeout(ctx, 300*time.Second)
	defer cancel()

	// 记录 requestID 到 URL 的映射，用于后续提取响应体
	reqMap := make(map[network.RequestID]string)

	// 在此处准备一个用于提取最终 token 的变量
	var extractedToken string
	gatewayHeaders := make(map[string]string)

	// 3. 监听网络请求
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch ev := ev.(type) {
		case *network.EventRequestWillBeSent:
			// 拦截登录后的任意 API 请求，从请求头中窃取 token
			if strings.Contains(ev.Request.URL, "gateway.ccopyright.com.cn") {
				for key, value := range ev.Request.Headers {
					gatewayHeaders[key] = fmt.Sprint(value)
				}
				if auth, ok := ev.Request.Headers["Authorization"]; ok {
					extractedToken = fmt.Sprint(auth)
				}
				if auth, ok := ev.Request.Headers["token"]; ok {
					extractedToken = fmt.Sprint(auth)
				}
			}
		case *network.EventResponseReceived:
			resp := ev.Response
			// 过滤 JSON 类型的响应
			if strings.Contains(resp.MimeType, "application/json") || strings.Contains(resp.MimeType, "text/json") {
				reqMap[ev.RequestID] = resp.URL
			}
		}
	})

	fmt.Println("正在启动浏览器...")

	// 4. 执行任务：开启网络监听，执行登录
	err := chromedp.Run(ctx,
		network.Enable(),
		// 第一步：访问登录页面并等待输入框
		chromedp.Navigate(loginURL),
		chromedp.WaitVisible(`input[placeholder="请输入用户名/手机号/邮箱"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`input[placeholder="请输入密码"]`, chromedp.ByQuery),

		// 填入账号密码
		chromedp.SendKeys(`input[placeholder="请输入用户名/手机号/邮箱"]`, username, chromedp.ByQuery),
		chromedp.SendKeys(`input[placeholder="请输入密码"]`, password, chromedp.ByQuery),

		// 为了防止太快被拦截或未触发 React 数据绑定，稍微延迟一下
		chromedp.Sleep(1*time.Second),

		// 点击登录
		chromedp.Click(`//*[contains(text(), '立即登录')]`, chromedp.BySearch),

		// 等待页面发生跳转或手动等待登录完成（因为可能需要过滑动验证码）
		// 我们在这里等待跳转到首页相关的标志出现，或者如果没标志，给长一点的时间让人工介入
		chromedp.ActionFunc(func(c context.Context) error {
			fmt.Println("请在弹出的浏览器中，如果有滑动验证码，请手动完成验证。等待登录成功并跳转...")
			return nil
		}),
	)
	if err != nil {
		log.Fatalf("执行登录阶段失败: %v", err)
	}

	// 尝试等待首页或仪表盘特征元素出现（代表登录成功）
	// 这里通过一个带超时的轮询等待页面 url 不再是 login.html
	err = chromedp.Run(ctx,
		chromedp.ActionFunc(func(c context.Context) error {
			for i := 0; i < 60; i++ { // 等待最多 60 秒
				var url string
				if err := chromedp.Location(&url).Do(c); err == nil {
					if !strings.Contains(url, "login.html") {
						return nil
					}
				}
				time.Sleep(1 * time.Second)
			}
			return fmt.Errorf("登录后等待跳转超时")
		}),
	)
	if err != nil {
		log.Fatalf("登录跳转失败: %v", err)
	}

	fmt.Println("登录成功！进入下一步点击操作...")

	// 确保抓取到了 token
	if extractedToken == "" {
		// 尝试从 localStorage 获取
		var lsToken string
		chromedp.Run(ctx, chromedp.Evaluate(`window.localStorage.getItem('token') || window.localStorage.getItem('Authorization')`, &lsToken))
		if lsToken != "" {
			extractedToken = lsToken
		}
	}

	fmt.Println("获取到的 Token:", extractedToken)

	fmt.Println("开始执行 UI 自动化操作...")
	err = chromedp.Run(ctx,
		// 0. 登录后出现的引导弹窗，点击“跳过”
		// 由于该弹窗可能不是每次都有，我们使用带超时的等待，如果找不到也不报错终止整个流程
		chromedp.ActionFunc(func(c context.Context) error {
			// 创建一个短超时的子上下文，只等待“跳过”按钮 3 秒
			timeoutCtx, cancel := context.WithTimeout(c, 3*time.Second)
			defer cancel()
			err := chromedp.WaitVisible(`//*[contains(text(), '跳过')]`, chromedp.BySearch).Do(timeoutCtx)
			if err == nil {
				fmt.Println("发现“跳过”按钮，检查是否同时存在“下一步”...")
				chromedp.Evaluate(`
					var skipEl = document.evaluate("//*[contains(text(), '跳过')]", document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue;
					var nextEl = document.evaluate("//*[contains(text(), '下一步')]", document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue;
					if (skipEl && nextEl) {
						skipEl.click();
					}
				`, nil).Do(c)
				time.Sleep(1 * time.Second)
			} else {
				fmt.Println("未发现或跳过了引导弹窗")
			}
			return nil // 不论是否找到，都继续下一步
		}),

		// 1. 点击 软件登记 (对应业务界面的入口)
		chromedp.ActionFunc(func(c context.Context) error {
			fmt.Println("正在等待『软件登记』元素出现...")

			// 使用较长的超时等待
			waitCtx, cancelWait := context.WithTimeout(c, 15*time.Second)
			defer cancelWait()

			// 优化 XPath，使用 text() 来精确匹配，避免匹配到外层 html 或 body
			errWait := chromedp.WaitVisible(`//li[contains(text(), '软件登记')]`, chromedp.BySearch).Do(waitCtx)
			if errWait != nil {
				fmt.Println("等待『软件登记』超时，正在保存当前 HTML 到 debug_page.html 以供调试...")
				var htmlContent string
				chromedp.OuterHTML("html", &htmlContent, chromedp.ByQuery).Do(c)
				os.WriteFile("debug_page.html", []byte(htmlContent), 0644)
			}
			return nil
		}),
		// 由于上一步 ActionFunc 中已经做了等待，这里我们直接执行 JS 点击
		// 改用 JS 点击，因为如果被其他透明层遮挡，JS 点击可以穿透
		chromedp.Evaluate(`
			var els = document.evaluate("//li[contains(text(), '软件登记')]", document, null, XPathResult.ANY_TYPE, null);
			var el = els.iterateNext();
			if (el) {
				el.click();
			}
		`, nil),
		chromedp.Sleep(2*time.Second),

		// 2. 选择 计算机软件著作权登记申请
		chromedp.WaitVisible(`//*[contains(text(), '计算机软件著作权登记申请')]`, chromedp.BySearch),
		chromedp.Click(`//*[contains(text(), '计算机软件著作权登记申请')]`, chromedp.BySearch),
		chromedp.Sleep(1*time.Second),

		// 点击 立即登记
		chromedp.WaitVisible(`//*[contains(text(), '立即登记')]`, chromedp.BySearch),
		chromedp.Click(`//*[contains(text(), '立即登记')]`, chromedp.BySearch),
		chromedp.Sleep(2*time.Second),

		// 3. 我是代理人
		chromedp.WaitVisible(`//*[contains(text(), '我是代理人')]`, chromedp.BySearch),
		chromedp.Click(`//*[contains(text(), '我是代理人')]`, chromedp.BySearch),
		chromedp.Sleep(1*time.Second),
		chromedp.Click(`//*[contains(text(), '确定')]`, chromedp.BySearch),
		chromedp.Sleep(2*time.Second),

		// 4. 软件名称与版本号
		chromedp.ActionFunc(func(c context.Context) error {
			fmt.Println("正在尝试查找并填写软件名称与版本号...")
			waitCtx, cancelWait := context.WithTimeout(c, 5*time.Second)
			defer cancelWait()

			// 尝试等待并查找软件全称输入框 (基于新的 HTML 结构使用 placeholder 定位)
			errSoft := chromedp.WaitVisible(`//input[@placeholder='请输入软件全称']`, chromedp.BySearch).Do(waitCtx)
			if errSoft != nil {
				fmt.Println("未找到原定的软件名称输入框，可能是页面结构已变更。正在导出页面 HTML 以供调试...")
				var htmlContent string
				if errHtml := chromedp.OuterHTML("html", &htmlContent, chromedp.ByQuery).Do(c); errHtml == nil {
					os.WriteFile("debug_agent_page.html", []byte(htmlContent), 0644)
					fmt.Println("已将当前页面保存至 debug_agent_page.html。请将此文件提供给 AI 以排查最新结构。")
				}
				// 此时跳过后续版本号的填写
				return nil
			}

			// 如果找到了软件名称，则执行输入
			chromedp.SendKeys(`//input[@placeholder='请输入软件全称']`, "测试测试", chromedp.BySearch).Do(c)
			time.Sleep(500 * time.Millisecond)

			// 查找并填写版本号 (基于新的 HTML 结构使用 placeholder 定位)
			errVer := chromedp.WaitVisible(`//input[@placeholder='请输入版本号']`, chromedp.BySearch).Do(waitCtx)
			if errVer == nil {
				chromedp.SendKeys(`//input[@placeholder='请输入版本号']`, "1.0.0", chromedp.BySearch).Do(c)
				time.Sleep(500 * time.Millisecond)
			}

			return nil
		}),

		// 点击 下一步
		chromedp.Click(`//*[contains(text(), '下一步')]`, chromedp.BySearch),
		chromedp.Sleep(2*time.Second),

		// 5. 著作权人 - 选择国家“中国”
		// 可能是下拉框，先点击 input 然后点击下拉项
		chromedp.WaitVisible(`//*[contains(text(), '国家')]/following::input[1] | //*[contains(text(), '国籍')]/following::input[1]`, chromedp.BySearch),
		chromedp.Click(`//*[contains(text(), '国家')]/following::input[1] | //*[contains(text(), '国籍')]/following::input[1]`, chromedp.BySearch),
		chromedp.Sleep(1*time.Second),
		chromedp.Click(`//li[contains(text(), '中国')] | //div[contains(@class, 'select') or contains(@class, 'dropdown')]//*[text()='中国']`, chromedp.BySearch),
		chromedp.Sleep(2*time.Second),
	)
	if err != nil {
		fmt.Printf("UI 操作执行失败 (如果页面结构不同可能会失败，可忽略并继续 API 获取): %v\n", err)
	} else {
		fmt.Println("UI 操作完成！")
	}

	var browserCookies []*network.Cookie
	err = chromedp.Run(ctx, chromedp.ActionFunc(func(c context.Context) error {
		var cookieErr error
		browserCookies, cookieErr = network.GetCookies().WithURLs([]string{
			loginURL,
			"https://gateway.ccopyright.com.cn/",
		}).Do(c)
		return cookieErr
	}))
	if err != nil {
		log.Fatalf("获取浏览器 Cookies 失败: %v", err)
	}

	rawJSON, err := fetchAllAreaRawJSON(ctx, extractedToken, browserCookies, gatewayHeaders)
	if err != nil {
		log.Fatalf("获取原始 JSON 失败: %v", err)
	}

	finalData, err := parseAndPrintData(rawJSON)
	if err != nil {
		log.Fatalf("转换省市区数据失败: %v", err)
	}

	// 将最终 JSON 写入项目根目录，避免大结果刷屏终端。
	out, err := json.MarshalIndent(finalData, "", "  ")
	if err != nil {
		log.Fatalf("JSON 序列化失败: %v", err)
	}
	const outputFile = "output.json"
	if err := os.WriteFile(outputFile, out, 0644); err != nil {
		log.Fatalf("写入结果文件失败: %v", err)
	}

	fmt.Printf("结果已写入: %s\n", outputFile)
	fmt.Printf("省份数量: %d\n", len(finalData))
	fmt.Println("任务结束。")
}

// parseAndPrintData 将抓取到的原始 JSON 转换为最终输出结构。
func parseAndPrintData(body []byte) ([]ProvinceLoc, error) {
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return nil, fmt.Errorf("未获取到省市区原始 JSON")
	}

	var raw []rawProvinceLoc
	if err := json.Unmarshal(body, &raw); err != nil {
		preview := string(body)
		if len(preview) > 300 {
			preview = preview[:300] + "..."
		}
		return nil, fmt.Errorf("原始 JSON 解析失败: %w; 长度=%d; 片段=%q", err, len(body), preview)
	}

	result := make([]ProvinceLoc, 0, len(raw))
	for _, p := range raw {
		prov := ProvinceLoc{
			Province:     p.Province,
			ProvinceCode: p.ProvinceCode,
			Cities:       make([]*CityLoc, 0, len(p.Cities)),
		}

		for _, c := range p.Cities {
			city := &CityLoc{
				City:      c.City,
				CityCode:  c.CityCode,
				Districts: make([]*DistrictLoc, 0, len(c.Districts)),
			}

			for _, d := range c.Districts {
				city.Districts = append(city.Districts, &DistrictLoc{
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

func fetchAllAreaRawJSON(ctx context.Context, token string, cookies []*network.Cookie, gatewayHeaders map[string]string) ([]byte, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("缺少可用 token，无法请求地区接口")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	const requestInterval = 200 * time.Millisecond
	authorizationKey := strings.TrimSpace(gatewayHeaders["authorization_key"])
	if authorizationKey == "" {
		authorizationKey = strings.TrimSpace(gatewayHeaders["Authorization_Key"])
	}
	if authorizationKey == "" {
		authorizationKey = findCookieValue(cookies, "authorization_key")
	}

	provinces, err := fetchAreaNodes(ctx, client, token, authorizationKey, cookies, gatewayHeaders, "https://gateway.ccopyright.com.cn/userServer/area/province/001")
	if err != nil {
		return nil, fmt.Errorf("获取省份失败: %w", err)
	}
	time.Sleep(requestInterval)

	raw := make([]rawProvinceLoc, 0, len(provinces))
	for _, p := range provinces {
		prov := rawProvinceLoc{
			Province:     p.Name,
			ProvinceCode: p.ID,
			Cities:       make([]rawCityLoc, 0),
		}

		if p.HasChildren == 1 {
			cities, err := fetchAreaNodes(ctx, client, token, authorizationKey, cookies, gatewayHeaders, "https://gateway.ccopyright.com.cn/userServer/area/city/"+p.ID+"/1")
			if err != nil {
				return nil, fmt.Errorf("获取省份 %s(%s) 下城市失败: %w", p.Name, p.ID, err)
			}
			time.Sleep(requestInterval)

			prov.Cities = make([]rawCityLoc, 0, len(cities))
			for _, c := range cities {
				city := rawCityLoc{
					City:      c.Name,
					CityCode:  c.ID,
					Districts: make([]rawDistrictLoc, 0),
				}

				if c.HasChildren == 1 {
					districts, err := fetchAreaNodes(ctx, client, token, authorizationKey, cookies, gatewayHeaders, "https://gateway.ccopyright.com.cn/userServer/area/city/"+c.ID+"/1")
					if err != nil {
						return nil, fmt.Errorf("获取城市 %s(%s) 下区县失败: %w", c.Name, c.ID, err)
					}
					time.Sleep(requestInterval)

					city.Districts = make([]rawDistrictLoc, 0, len(districts))
					for _, d := range districts {
						city.Districts = append(city.Districts, rawDistrictLoc{
							District:     d.Name,
							DistrictCode: d.ID,
						})
					}
				}

				prov.Cities = append(prov.Cities, city)
			}
		}

		raw = append(raw, prov)
	}

	body, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("序列化原始 JSON 失败: %w", err)
	}
	return body, nil
}

func fetchAreaNodes(ctx context.Context, client *http.Client, token, authorizationKey string, cookies []*network.Cookie, gatewayHeaders map[string]string, url string) ([]areaNode, error) {
	const maxAttempts = 4
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}

		for key, value := range gatewayHeaders {
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
		req.Header.Set("token", token)
		req.Header.Set("Authorization", token)
		if authorizationKey != "" {
			req.Header.Set("authorization_key", authorizationKey)
		}
		if req.Header.Get("Cookie") == "" {
			if cookieHeader := buildCookieHeader(cookies); cookieHeader != "" {
				req.Header.Set("Cookie", cookieHeader)
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			if attempt == maxAttempts || !shouldRetryAreaRequest(0, err) {
				return nil, err
			}
			waitBeforeRetry(ctx, attempt)
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt == maxAttempts || !shouldRetryAreaRequest(0, readErr) {
				return nil, readErr
			}
			waitBeforeRetry(ctx, attempt)
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
			waitBeforeRetry(ctx, attempt)
			continue
		}

		var result areaAPIResponse
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

func waitBeforeRetry(ctx context.Context, attempt int) {
	delay := time.Duration(attempt) * time.Second
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func findCookieValue(cookies []*network.Cookie, name string) string {
	for _, cookie := range cookies {
		if cookie != nil && cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func buildCookieHeader(cookies []*network.Cookie) string {
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil || cookie.Name == "" {
			continue
		}
		value := sanitizeCookieValue(cookie.Value)
		parts = append(parts, cookie.Name+"="+value)
	}
	return strings.Join(parts, "; ")
}

func sanitizeCookieValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"`)
	value = strings.ReplaceAll(value, "\n", "")
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, ";", "")
	return value
}

func isAreaAPIError(result areaAPIResponse) bool {
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
