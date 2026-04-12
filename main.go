package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// ProvinceLoc 省份层级
type ProvinceLoc struct {
	Province     string     `json:"Province"`
	ProvinceCode string     `json:"-"`
	Cities       []*CityLoc `json:"Cities"`
}

// CityLoc 城市层级
type CityLoc struct {
	City      string         `json:"City"`
	CityCode  string         `json:"-"`
	Districts []*DistrictLoc `json:"Districts"`
}

// DistrictLoc 区县层级
type DistrictLoc struct {
	District     string `json:"District"`
	DistrictCode string `json:"-"`
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

	// 3. 监听网络请求
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch ev := ev.(type) {
		case *network.EventRequestWillBeSent:
			// 拦截登录后的任意 API 请求，从请求头中窃取 token
			if strings.Contains(ev.Request.URL, "gateway.ccopyright.com.cn") {
				if auth, ok := ev.Request.Headers["Authorization"]; ok {
					extractedToken = auth.(string)
				}
				if auth, ok := ev.Request.Headers["token"]; ok {
					extractedToken = auth.(string)
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
		// 采用 XPath 查找包含对应文本的元素的最近一个 input
		chromedp.WaitVisible(`//label[contains(text(), '软件')]/following::input[1] | //*[contains(text(), '软件名称')]/following::input[1]`, chromedp.BySearch),
		chromedp.SendKeys(`//label[contains(text(), '软件')]/following::input[1] | //*[contains(text(), '软件名称')]/following::input[1]`, "测试测试", chromedp.BySearch),
		chromedp.Sleep(500*time.Millisecond),

		chromedp.WaitVisible(`//label[contains(text(), '版本')]/following::input[1] | //*[contains(text(), '版本号')]/following::input[1]`, chromedp.BySearch),
		chromedp.SendKeys(`//label[contains(text(), '版本')]/following::input[1] | //*[contains(text(), '版本号')]/following::input[1]`, "1.0.0", chromedp.BySearch),
		chromedp.Sleep(500*time.Millisecond),

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

	// 由于发现后端 API 是树状逐级请求的：
	// 省份：https://gateway.ccopyright.com.cn/userServer/area/province/001
	// 城市/区县：https://gateway.ccopyright.com.cn/userServer/area/city/{provinceId}/1
	// (或者可能是 area/city/001011/1 包含城市，hasChildren 标志是否还有下一级)
	// 我们将直接使用 chromedp 发起 fetch 请求获取完整树

	var finalData []ProvinceLoc
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`
			(async function(token) {
				const headers = {
					'Content-Type': 'application/json',
					'token': token,
					'Authorization': token
				};
				
				// 获取所有省份
				const provResp = await fetch('https://gateway.ccopyright.com.cn/userServer/area/province/001', {headers});
				const provData = await provResp.json();
				const provinces = provData.data || [];
				
				let result = [];
				for (let p of provinces) {
					let provItem = {
						Province: p.name,
						ProvinceCode: p.id,
						Cities: []
					};
					
					// 如果该省份有子节点（市/区）
					if (p.hasChildren === 1) {
						const cityResp = await fetch('https://gateway.ccopyright.com.cn/userServer/area/city/' + p.id + '/1', {headers});
						const cityData = await cityResp.json();
						const cities = cityData.data || [];
						
						for (let c of cities) {
							let cityItem = {
								City: c.name,
								CityCode: c.id,
								Districts: []
							};
							
							// 如果城市有区县，继续请求
							if (c.hasChildren === 1) {
								const distResp = await fetch('https://gateway.ccopyright.com.cn/userServer/area/city/' + c.id + '/1', {headers});
								const distData = await distResp.json();
								const dists = distData.data || [];
								
								for (let d of dists) {
									cityItem.Districts.push({
										District: d.name,
										DistrictCode: d.id
									});
								}
							}
							provItem.Cities.push(cityItem);
						}
					}
					result.push(provItem);
				}
				return result;
			})('`+extractedToken+`')
		`, &finalData),
	)

	if err != nil {
		log.Fatalf("执行脚本获取数据失败: %v", err)
	}

	// 输出最终 JSON
	out, err := json.MarshalIndent(finalData, "", "  ")
	if err != nil {
		log.Fatalf("JSON 序列化失败: %v", err)
	}
	fmt.Println("\n=== 最终抓取的省市区数据 ===")
	fmt.Println(string(out))
	fmt.Println("任务结束。")
}

// parseAndPrintData 解析并打印数据的占位符函数
func parseAndPrintData(body []byte) {
	// TODO: 由于缺少真实的 API 返回 JSON 结构，这里使用占位符逻辑。
	// 请你根据下方打印的 `原始 JSON 数据片段`，修改此函数，
	// 将原始数据反序列化，并映射到 []ProvinceLoc 结构中。

	fmt.Println("=== 原始 JSON 数据片段 (前 500 个字符) ===")
	if len(body) > 500 {
		fmt.Println(string(body)[:500], "...")
	} else {
		fmt.Println(string(body))
	}
	fmt.Println("==========================================")

	/*
		// 示例映射逻辑：

		// 1. 定义与 API 返回结构匹配的原始结构体
		type RawResponse struct {
			Code int `json:"code"`
			Data []struct {
				ProvName string `json:"prov_name"`
				ProvCode string `json:"prov_code"`
				CityList []struct {
					CityName string `json:"city_name"`
					CityCode string `json:"city_code"`
					AreaList []struct {
						AreaName string `json:"area_name"`
						AreaCode string `json:"area_code"`
					} `json:"area_list"`
				} `json:"city_list"`
			} `json:"data"`
		}

		var raw RawResponse
		if err := json.Unmarshal(body, &raw); err != nil {
			fmt.Printf("解析 JSON 失败: %v\n", err)
			return
		}

		// 2. 转换为目标 []ProvinceLoc 结构
		var result []ProvinceLoc
		for _, p := range raw.Data {
			prov := ProvinceLoc{
				Province:     p.ProvName,
				ProvinceCode: p.ProvCode,
			}
			for _, c := range p.CityList {
				city := &CityLoc{
					City:     c.CityName,
					CityCode: c.CityCode,
				}
				for _, a := range c.AreaList {
					dist := &DistrictLoc{
						District:     a.AreaName,
						DistrictCode: a.AreaCode,
					}
					city.Districts = append(city.Districts, dist)
				}
				prov.Cities = append(prov.Cities, city)
			}
			result = append(result, prov)
		}

		// 3. 输出目标 JSON 格式
		out, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			fmt.Printf("目标 JSON 序列化失败: %v\n", err)
			return
		}
		fmt.Println("=== 最终输出 ===")
		fmt.Println(string(out))
	*/
}
