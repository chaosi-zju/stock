package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	"github.com/robfig/cron/v3"
	"golang.org/x/net/html"
)

const (
	ContentSelector        = ".t.z"
	ThreadTextAreaSelector = "#textarea"
	UserInfoSelector       = ".pwB_uConside_a"
)

// 回帖的内容
var ReplyContents = []string{
	"谢谢分享！",
	"感谢分享！！",
	"顶楼主，谢谢！！！",
	"支持楼主一波！",
	"必需支持！",
	"支持楼主，非常感谢！",
}

// 全局变量，用于存储日志文件
var currentLogFile *os.File

// 全局任务状态和调度器
var (
	todayCheckInSuccess bool
	lastCheckInDate     string

	taskMutex       sync.Mutex
	isTaskRunning   bool
	lastRunTime     time.Time
	lastSuccessTime time.Time
	scheduler       *cron.Cron
	retryTimer      *time.Timer
	todayRetryTimes int
	userName        string
)

// env变量
var (
	BaseURL         string
	LoginSection    string
	ReplySection    string
	CheckInSection  string
	UserInfoSection string
	EnableTelegram  bool
	MyBotToken      string
	ChatID          int64
	EnableHeadless  bool
	WaitingTime     int
	RetryInterval   time.Duration
	EnableCron      bool
	CronSchedule    string
	RunOnStart      bool
)

// Browser 结构体封装了 chromedp 的执行上下文，用于后续多步操作
type Browser struct {
	ctx    context.Context
	cancel context.CancelFunc
	cmd    *exec.Cmd // 记录 Chrome 进程
}

// init 用于初始化环境变量
func init() {
	// 加载 .env 文件
	if err := godotenv.Load(); err != nil {
		log.Fatalf("加载 .env 文件失败: %v", err)
	}

	// 提取用户名
	if os.Getenv("RUN_MODE") == "finishNewManTask" {
		userName = os.Getenv("NEWMAN_USERNAME")
	} else {
		userName = os.Getenv("FORUM_USERNAME")
	}

	// 初始化配置变量
	BaseURL = os.Getenv("BASE_URL")
	LoginSection = os.Getenv("LOGIN_SECTION")
	ReplySection = os.Getenv("REPLY_SECTION")
	if os.Getenv("RUN_MODE") == "finishNewManTask" {
		ReplySection = os.Getenv("NEWMAN_SECTION")
	}
	CheckInSection = os.Getenv("CHECK_IN_SECTION")
	UserInfoSection = os.Getenv("USER_INFO_SECTION")

	// 转化 ENABLE_TELEGRAM 为 bool
	if enableTelegramStr := os.Getenv("ENABLE_TELEGRAM"); enableTelegramStr != "" {
		if enableTelegram, err := strconv.ParseBool(enableTelegramStr); err == nil {
			EnableTelegram = enableTelegram
		}
	}
	MyBotToken = os.Getenv("TELEGRAM_BOT_TOKEN")

	// 转换 TELEGRAM_CHAT_ID 为 int64
	if chatIDStr := os.Getenv("TELEGRAM_CHAT_ID"); chatIDStr != "" {
		if id, err := strconv.ParseInt(chatIDStr, 10, 64); err == nil {
			ChatID = id
		}
	}

	// 转化 ENABLE_HEADLESS 为 bool
	if enableHeadlessStr := os.Getenv("ENABLE_HEADLESS"); enableHeadlessStr != "" {
		if enable, err := strconv.ParseBool(enableHeadlessStr); err == nil {
			EnableHeadless = enable
		}
	}

	// 转化 WAITING_TIME 为 int
	if waitingTimeStr := os.Getenv("WAITING_TIME"); waitingTimeStr != "" {
		if waitingTime, err := strconv.Atoi(waitingTimeStr); err == nil {
			WaitingTime = waitingTime
		}
	}

	// 转化 ENABLE_CRON 为 bool
	if enableCronStr := os.Getenv("ENABLE_CRON"); enableCronStr != "" {
		if enableCron, err := strconv.ParseBool(enableCronStr); err == nil {
			EnableCron = enableCron
		}
	}

	CronSchedule = os.Getenv("CRON_SCHEDULE")

	// 转化 RETRY_INTERVAL 为 duration
	if retryIntervalStr := os.Getenv("RETRY_INTERVAL"); retryIntervalStr != "" {
		if minutes, err := strconv.Atoi(retryIntervalStr); err == nil {
			RetryInterval = time.Duration(minutes) * time.Minute
		} else {
			// 尝试作为带单位的时间解析
			if duration, err := time.ParseDuration(retryIntervalStr); err == nil {
				RetryInterval = duration
			} else {
				log.Printf("无法解析重试间隔 '%s'，使用默认值30分钟", retryIntervalStr)
				RetryInterval = 30 * time.Minute
			}
		}
	} else {
		RetryInterval = 30 * time.Minute // 默认重试间隔为30分钟
	}

	// 转化 RUN_ON_START 为 bool
	if runOnStartStr := os.Getenv("RUN_ON_START"); runOnStartStr != "" {
		if runOnStart, err := strconv.ParseBool(runOnStartStr); err == nil {
			RunOnStart = runOnStart
		}
	}

	// 配置日志
	setupLogger()
}

// 设置日志
func setupLogger() {
	// 关闭之前的日志文件
	if currentLogFile != nil {
		currentLogFile.Close()
	}

	// 确保logs目录存在
	os.MkdirAll("logs", 0755)

	// 清理旧日志
	cleanupOldLogs(7)

	// 创建日志文件(日期为当天--当天+7天)
	logFileName := fmt.Sprintf("logs/hjd2048_daysign_%s.log", time.Now().Format("2006-01-02"))
	logFile, err := os.OpenFile(logFileName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("无法创建日志文件: %v", err)
		return
	}

	// 同时输出到控制台和文件
	log.SetOutput(io.MultiWriter(os.Stdout, logFile))
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// 保存当前日志文件指针
	currentLogFile = logFile
}

// 清理超过指定天数的旧日志
func cleanupOldLogs(daysToKeep int) {
	files, err := os.ReadDir("logs")
	if err != nil {
		log.Printf("读取日志目录失败: %v", err)
		return
	}

	// 计算截止日期
	cutoffDate := time.Now().AddDate(0, 0, -daysToKeep)

	// 日志文件名格式正则表达式
	logFilePattern := regexp.MustCompile(`hjd2048_daysign_(\d{4}-\d{2}-\d{2})\.log`)

	removed := 0
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		// 匹配日志文件名
		matches := logFilePattern.FindStringSubmatch(file.Name())
		if len(matches) < 2 {
			continue
		}

		// 解析日志文件日期
		fileDate, err := time.Parse("2006-01-02", matches[1])
		if err != nil {
			log.Printf("无法解析日志文件日期 %s: %v", file.Name(), err)
			continue
		}

		// 如果文件日期早于截止日期，删除文件
		if fileDate.Before(cutoffDate) {
			if err := os.Remove(filepath.Join("logs", file.Name())); err != nil {
				log.Printf("删除过期日志文件 %s 失败: %v", file.Name(), err)
			} else {
				log.Printf("已删除过期日志文件: %s", file.Name())
				removed++
			}
		}
	}

	if removed > 0 {
		log.Printf("共清理了 %d 个过期日志文件", removed)
	}
}

// executeTask 执行完整的任务流程，任何步骤失败都会导致整个任务失败
func executeTask() {
	// 检查任务是否已经在运行
	taskMutex.Lock()
	if isTaskRunning {
		log.Println("任务已在运行中，跳过本次执行")
		taskMutex.Unlock()
		os.Exit(2)
	}
	// 如果距离上次执行时间不足5分钟，跳过本次执行
	if !lastRunTime.IsZero() && time.Since(lastRunTime) < 5*time.Minute {
		log.Printf("距离上次执行仅 %v，小于5分钟，跳过本次执行", time.Since(lastRunTime))
		taskMutex.Unlock()
		os.Exit(3)
	}

	// 获取当前日期
	currentDate := time.Now().Format("2006-01-02")

	// 检查是否是新的一天，如果是则重置签到状态
	if currentDate != lastCheckInDate {
		todayCheckInSuccess = false
		lastCheckInDate = currentDate
		todayRetryTimes = 0
	}

	// 如果今天已经成功签到，直接返回，不执行任务
	if todayCheckInSuccess {
		taskMutex.Unlock()
		os.Exit(4)
	}

	// 更新任务状态
	isTaskRunning = true
	lastRunTime = time.Now()
	taskMutex.Unlock()

	// 函数结束时清理状态
	defer func() {
		taskMutex.Lock()
		isTaskRunning = false
		taskMutex.Unlock()
	}()

	log.Println("开始执行任务...")

	// 收集任务结果
	var message strings.Builder
	currentTime := time.Now().Format("2006年01月02日 15:04:05")
	message.WriteString(fmt.Sprintf("%s 任务开始\n", currentTime))

	// 创建浏览器实例
	browser, err := NewBrowser()
	if err != nil {
		log.Printf("创建浏览器实例失败: %v", err)
		success := scheduleRetry("创建浏览器失败: " + err.Error())
		if !success {
			os.Exit(5)
		}
	}

	// 确保无论如何浏览器都会被关闭
	browserClosed := false
	defer func() {
		if !browserClosed {
			log.Println("关闭浏览器实例...")
			browser.Close()
		}
	}()

	// 1. 访问论坛回帖页面
	replyURL := BaseURL + ReplySection
	if err = browser.NavigateTo(replyURL); err != nil {
		log.Printf("导航回帖页失败: %v", err)
		success := scheduleRetry("导航回帖页失败: " + err.Error())
		if !success {
			os.Exit(6)
		}
	}

	// 2. 检查登陆状态
	if err = browser.CheckLoginStatus(); err != nil {
		log.Printf("检查登陆状态出错：%v", err)
		success := scheduleRetry("检查登陆状态出错: " + err.Error())
		if !success {
			os.Exit(7)
		}
	}

	// 3. 获取第一个符合条件的帖子数据
	postTitle, href, err := browser.GetFirstPost()
	if err != nil {
		log.Printf("提取数据失败: %v", err)
		success := scheduleRetry("提取数据失败: " + err.Error())
		if !success {
			os.Exit(8)
		}
	}

	// 4. 打开帖子
	fullURL := BaseURL + href
	if err = browser.NavigateTo(fullURL); err != nil {
		log.Printf("打开帖子失败: %v", err)
		success := scheduleRetry("打开帖子失败: " + err.Error())
		if !success {
			os.Exit(9)
		}
	}

	// 5. 回帖
	replyContent, err := browser.ReplyPost()
	if err != nil {
		log.Printf("回帖失败: %v", err)
		success := scheduleRetry("回帖失败: " + err.Error())
		if !success {
			os.Exit(10)
		}
	}
	log.Printf("成功回复帖子: \n标题：%s, \n回帖：%s", postTitle, replyContent)

	// 6. 签到
	checkInResult, err := browser.CheckIn()
	if err != nil {
		log.Printf("签到失败: %v", err)
		success := scheduleRetry("签到失败: " + err.Error())
		if !success {
			os.Exit(11)
		}
	}
	todayCheckInSuccess = strings.Contains(checkInResult, "签到成功") || strings.Contains(checkInResult, "今日已经签到")

	// 7. 获取用户信息
	userInfo, err := browser.GetUserInfo()
	if err != nil {
		log.Printf("获取用户信息失败: %v", err)
		//scheduleRetry("获取用户信息失败: " + err.Error())
		//return
	}

	replyInfo := fmt.Sprintf("成功回复帖子: \n标题：%s, \n回帖：%s", postTitle, replyContent)

	//8. 发送通知
	notificationMsg := fmt.Sprintf(
		"✅ hjd2048 ✅，\n时间: %s\n%s\n%s\n%s",
		time.Now().Format("2006-01-02 15:04:05"),
		replyInfo,
		checkInResult,
		userInfo,
	)
	if EnableTelegram {
		if err := SendTelegramNotification(notificationMsg); err != nil {
			log.Printf("发送通知失败: %v\n通知内容: %s", err, notificationMsg)
			//scheduleRetry("发送通知失败: " + err.Error())
			//return
		}
	} else {
		log.Printf("发送通知: %s", notificationMsg)
	}

	// 任务成功，更新上次成功时间
	taskMutex.Lock()
	lastSuccessTime = time.Now()
	taskMutex.Unlock()

	// 在函数结束前明确关闭浏览器
	log.Println("任务完成，关闭浏览器...")
	browser.Close()
	browserClosed = true
}

// scheduleRetry 安排任务重试
func scheduleRetry(reason string) (success bool) {
	todayRetryTimes++
	// 获取当前日期
	currentDate := time.Now().Format("2006-01-02")

	taskMutex.Lock()
	// 如果今天已经成功签到，不安排重试
	if todayCheckInSuccess && currentDate == lastCheckInDate {
		log.Printf("今天已经成功签到，不重试: %s", reason)
		taskMutex.Unlock()
		return
	}
	taskMutex.Unlock()

	log.Printf("任务失败，原因: %s，将在 %v 后重试", reason, RetryInterval)

	// 取消之前的重试计时器（如果存在）
	if retryTimer != nil {
		retryTimer.Stop()
	}

	// 设置新的重试计时器
	retryTimer = time.AfterFunc(RetryInterval, func() {
		// 重试前再次检查是否已成功签到
		currentDate := time.Now().Format("2006-01-02")
		taskMutex.Lock()
		alreadySuccess := todayCheckInSuccess && currentDate == lastCheckInDate
		taskMutex.Unlock()

		if alreadySuccess {
			log.Println("定时重试前检测到今天已经成功签到，取消重试")
			return
		}

		if todayRetryTimes > 3 {
			log.Printf("已重试 %d 次，仍未成功签到，放弃重试", todayRetryTimes)
			return
		}

		log.Println("开始重试任务...")
		executeTask()
	})

	// 发送失败通知
	failureMsg := fmt.Sprintf(
		"❌ 任务失败 ❌\n时间: %s\n原因: %s\n将在 %d 分钟后重试",
		time.Now().Format("2006-01-02 15:04:05"),
		reason,
		int(RetryInterval.Minutes()),
	)

	// 发送失败通知
	if EnableTelegram {
		if err := SendTelegramNotification(failureMsg); err != nil {
			log.Printf("发送失败通知失败: %v", err)
		}
	} else {
		log.Printf("发送失败通知: %s", failureMsg)
	}

	return true
}

// startScheduler 启动定时调度器
func startScheduler() {
	scheduler = cron.New(cron.WithSeconds())

	// 添加定时任务
	_, err := scheduler.AddFunc(CronSchedule, executeTask)
	if err != nil {
		log.Fatalf("添加定时任务失败: %v", err)
	}

	// 启动调度器
	scheduler.Start()
}

// NewBrowser 创建新的浏览器实例，并启动浏览器，确保上下文可用
func NewBrowser() (*Browser, error) {
	// 从环境变量中获取Chrome路径
	chromePath := os.Getenv("CHROME_PATH")
	if chromePath == "" {
		// 尝试几个常见的路径
		possiblePaths := []string{
			"/snap/bin/chromium",
			"chromium",
			"google-chrome",
			"chromium-browser",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/usr/bin/google-chrome",
		}

		for _, path := range possiblePaths {
			// 使用 which 命令检查可执行文件是否存在
			cmd := exec.Command("which", path)
			if err := cmd.Run(); err == nil {
				chromePath = path
				log.Printf("自动检测到Chrome路径: %s", chromePath)
				break
			}
		}

		if chromePath == "" {
			log.Println("未找到Chrome可执行文件，请设置CHROME_PATH环境变量")
		}
	} else {
		log.Printf("使用环境变量中配置的Chrome路径: %s", chromePath)
	}

	// 强制杀死所有可能残留的 Chrome 进程
	if os.Getenv("FORCE_KILL_CHROME") == "true" {
		killPreviousChrome()
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("headless", EnableHeadless),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-software-rasterizer", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-infobars", true),
		chromedp.Flag("disable-notifications", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("ignore-certificate-errors", true),
		chromedp.Flag("disable-popup-blocking", true),
		chromedp.Flag("incognito", true),
		chromedp.Flag("disable-translate", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.ExecPath(chromePath),
	)

	// 创建分配器上下文
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	// 创建 Chrome 上下文
	ctx, cancelCtx := chromedp.NewContext(allocCtx, chromedp.WithLogf(log.Printf))
	// 启动浏览器（空任务），确保 ctx 正常启动
	if err := chromedp.Run(ctx); err != nil {
		cancelCtx()
		cancelAlloc()
		return nil, err
	}
	// 合并取消函数
	combinedCancel := func() {
		cancelCtx()
		cancelAlloc()
	}
	return &Browser{
		ctx:    ctx,
		cancel: combinedCancel,
	}, nil
}

// 修改监控函数以支持退出
func monitorChromeProcesses(stop chan struct{}) {
	log.Println("开始监控Chrome进程...")
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	// 立即执行一次检查
	checkChromeProcesses()

	for {
		select {
		case <-ticker.C:
			checkChromeProcesses()
		case <-stop:
			log.Println("Chrome进程监控已停止")
			return
		}
	}
}

// 检查Chrome进程数量并在必要时清理
func checkChromeProcesses() {
	var cmd *exec.Cmd
	var output []byte
	var err error
	var count int

	if runtime.GOOS == "windows" {
		cmd = exec.Command("tasklist", "/FI", "IMAGENAME eq chrome.exe", "/NH")
		output, err = cmd.Output()
		if err == nil {
			// Windows: 计算输出中"chrome.exe"的行数
			count = strings.Count(string(output), "chrome.exe")
		}
	} else {
		// Linux/macOS: 使用 pgrep 获取进程数量
		cmd = exec.Command("pgrep", "-c", "chrom")
		output, err = cmd.Output()
		if err == nil && len(output) > 0 {
			count, _ = strconv.Atoi(strings.TrimSpace(string(output)))
		}
	}

	// 如果出现错误，可能是因为没有找到任何进程
	if err != nil {
		log.Printf("检查Chrome进程状态: 未发现Chrome进程或执行命令失败: %v", err)
		return
	}

	log.Printf("检测到 %d 个Chrome相关进程", count)

	// 如果进程数量超过阈值，则进行清理
	if count > 5 {
		log.Printf("Chrome进程数量(%d)超过阈值，执行清理...", count)
		killPreviousChrome()

		// 清理后再次检查
		time.Sleep(5 * time.Second)
		checkChromeProcesses()
	}
}

// 改进强制终止Chrome进程的函数
func killPreviousChrome() {
	log.Println("正在终止残留的Chrome进程...")

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("taskkill", "/F", "/IM", "chrome.exe", "/IM", "chromium.exe")
	} else if runtime.GOOS == "darwin" {
		// macOS 特殊处理
		cmd = exec.Command("pkill", "-9", "-f", "Google Chrome")
		cmd.Run() // 忽略错误
		cmd = exec.Command("pkill", "-9", "-f", "Chromium")
	} else {
		// Linux
		cmd = exec.Command("pkill", "-9", "-f", "chrom")
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		// 进程不存在时不报错
		if !strings.Contains(string(output), "没有找到") &&
			!strings.Contains(string(output), "not found") {
			log.Printf("终止Chrome进程时出现错误: %v", err)
		}
	} else {
		log.Println("成功终止Chrome进程")
	}
}

// Close 关闭浏览器实例
func (b *Browser) Close() {
	b.cancel()
}

// Execute 用于执行一组 chromedp.Action，并设置一个超时
func (b *Browser) Execute(actions ...chromedp.Action) error {
	ctx, cancel := context.WithTimeout(b.ctx, 60*time.Second)
	defer cancel()
	return chromedp.Run(ctx, actions...)
}

// NavigateTo 导航到指定页面
func (b *Browser) NavigateTo(url string) error {
	return b.Execute(chromedp.Navigate(url))
}

// WaitForElement 等待页面中指定的元素可见
func (b *Browser) WaitForElement(selector string) error {
	return b.Execute(chromedp.WaitVisible(selector))
}

// GetHTML 获取指定 js 路径对应的HTML内容
func (b *Browser) GetHTML(sel string) (string, error) {
	var html string
	err := b.Execute(chromedp.OuterHTML(sel, &html, chromedp.ByQuery))
	return html, err
}

// Click 模拟点击操作
func (b *Browser) Click(selector string) error {
	return b.Execute(chromedp.Click(selector, chromedp.ByQuery))
}

// Input 模拟输入文本
func (b *Browser) Input(selector, text string) error {
	return b.Execute(
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.SendKeys(selector, text, chromedp.ByQuery),
	)
}

// GetFirstPost 从页面 HTML 中提取“广告连接”后第一个符合条件的帖子数据
func (b *Browser) GetFirstPost() (title string, href string, err error) {
	// 访问论坛回帖页面
	replyURL := BaseURL + ReplySection
	// 访问论坛回帖页面并提取帖子数据
	if err = b.NavigateTo(replyURL); err != nil {
		log.Printf("导航回帖页失败: %v", err)
		return
	}
	if err = b.WaitForElement(ContentSelector); err != nil {
		log.Printf("等待元素失败: %v", err)
		return
	}
	htmlContent, err := b.GetHTML("body")
	if err != nil {
		log.Printf("获取HTML失败: %v", err)
		return
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return "", "", err
	}

	// 定位 table#ajaxtable 下的第二个 tbody
	tbody := doc.Find("table#ajaxtable tbody").Eq(1)
	if tbody.Length() == 0 {
		return "", "", errors.New("未找到第二个 tbody")
	}

	var target *goquery.Selection
	found := false
	// 遍历 tbody 的所有子节点，查找注释节点包含“广告连接”
	tbody.Contents().EachWithBreak(func(i int, s *goquery.Selection) bool {
		for _, node := range s.Nodes {
			if node.Type == html.CommentNode && strings.Contains(node.Data, "广告连接") {
				// 从该注释节点向后查找第一个符合条件的 tr
				for i := 0; i <= 5; i++ {
					target = s.NextFiltered("tr.tr3.t_one").Eq(i)
					targetHTML, _ := target.Html()
					if strings.Contains(targetHTML, "admin") {
						log.Printf("跳过包含 admin 的帖子: %s", targetHTML)
						continue
					}
					if target.Length() > 0 {
						found = true
						return false // 找到后退出遍历
					}
				}
			}
		}
		return true
	})
	if !found || target == nil || target.Length() == 0 {
		return "", "", errors.New("未找到广告连接后的帖子")
	}

	// 根据帖子的结构，假设帖子的标题链接在 target 内的 a.subject 中
	a := target.Find("a.subject").First()
	if a.Length() == 0 {
		return "", "", errors.New("未找到帖子的链接元素")
	}
	title = strings.TrimSpace(a.Text())
	href, exists := a.Attr("href")
	if !exists {
		return "", "", errors.New("帖子链接中没有 href 属性")
	}
	return title, href, nil
}

// GetIndexedPost 从页面 HTML 中提取“广告连接”后第 index 个符合条件的帖子数据
func (b *Browser) GetIndexedPost(index int) (pre int, title string, href string, err error) {
	// 访问论坛回帖页面
	replyURL := BaseURL + ReplySection
	// 访问论坛回帖页面并提取帖子数据
	if err = b.NavigateTo(replyURL); err != nil {
		log.Printf("导航回帖页失败: %v", err)
		return
	}
	if err = b.WaitForElement(ContentSelector); err != nil {
		log.Printf("等待元素失败: %v", err)
		return
	}
	htmlContent, err := b.GetHTML("body")
	if err != nil {
		log.Printf("获取HTML失败: %v", err)
		return
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return index, "", "", err
	}

	// 定位 table#ajaxtable 下的第二个 tbody
	tbody := doc.Find("table#ajaxtable tbody").Eq(1)
	if tbody.Length() == 0 {
		return index, "", "", errors.New("未找到第二个 tbody")
	}

	var target *goquery.Selection
	found := false
	// 遍历 tbody 的所有子节点，查找注释节点包含“广告连接”
	tbody.Contents().EachWithBreak(func(i int, s *goquery.Selection) bool {
		if i <= index {
			return true
		}
		// 从该节点向后查找第一个符合条件的 tr
		target = s.NextFiltered("tr.tr3.t_one").First()
		targetHTML, _ := target.Html()
		if targetHTML == "" {
			return true
		}
		if strings.Contains(targetHTML, "admin") {
			log.Printf("跳过包含 admin 的帖子: %s", targetHTML)
			return true
		}
		if target.Length() > 0 {
			pre = i
			found = true
			return false // 找到后退出遍历
		}
		return true
	})
	if !found || target == nil || target.Length() == 0 {
		return index, "", "", errors.New("未找到广告连接后的帖子")
	}

	// 根据帖子的结构，假设帖子的标题链接在 target 内的 a.subject 中
	a := target.Find("a.subject").First()
	if a.Length() == 0 {
		return index, "", "", errors.New("未找到帖子的链接元素")
	}
	title = strings.TrimSpace(a.Text())
	href, exists := a.Attr("href")
	if !exists {
		return index, "", "", errors.New("帖子链接中没有 href 属性")
	}
	return pre, title, href, nil
}

// 检查登陆状态是否有效，若无效则执行登陆并加载cookie
func (b *Browser) CheckLoginStatus() error {
	// 等待 header 元素加载
	if err := b.WaitForElement("div.header_up_sign"); err != nil {
		return err
	}
	// 获取 header 的 HTML 内容（如果页面中有多个 div.header_up_sign，这里取第一个）
	headerHTML, err := b.GetHTML("div.header_up_sign")
	if err != nil {
		return err
	}

	// 检查 cookies 文件是否存在且未过期（不超过7天）
	needLogin := false
	cookiesExpired := false

	// 如果 header 包含"登录"且不包含"退出"，认为未登录
	if strings.Contains(headerHTML, "登录") && !strings.Contains(headerHTML, "退出") {
		needLogin = true
	}

	// 检查 cookies 文件是否存在
	fileInfo, err := os.Stat("./cookies")
	if err != nil {
		// cookies 文件不存在
		needLogin = true
	} else {
		// 检查 cookies 文件的修改时间，如果超过7天则视为过期
		if time.Since(fileInfo.ModTime()).Hours() > 24*7 {
			log.Printf("cookies 已过期（超过7天），需要重新登录")
			cookiesExpired = true
			needLogin = true
		}
	}

	// 如果 cookies 过期，删除文件
	if cookiesExpired {
		err := os.Remove("./cookies")
		if err != nil {
			log.Printf("删除过期 cookies 文件失败: %v", err)
		} else {
			log.Printf("已删除过期 cookies 文件")
		}
	}

	if needLogin {
		// 执行登录操作
		if err := b.Login(); err != nil {
			return err
		}
		// 登录成功后，保存 cookies 到文件
		cookiesFile := b.SaveCookies()
		log.Printf("登录成功，cookies 已保存到 %s", cookiesFile)
	} else if fileInfo != nil && fileInfo.Size() > 0 {
		// cookies 文件存在且不为空，执行 setCookies 操作
		if err := b.SetCookies(); err != nil {
			return err
		}
		log.Printf("使用已有的 cookies 登录成功")
	} else {
		log.Printf("检测到已登录状态")
	}
	return nil
}

// 填写登录表单中：用户名、密码、安全问题（选择“我的中学校名”，value="4"）、答案
func (b *Browser) Login() error {
	// 直接导航到首页（index.html），因为登录表单在首页中
	if err := b.NavigateTo(BaseURL + LoginSection); err != nil {
		return err
	}

	// 等待登录表单区域加载
	if err := b.WaitForElement(".cc.p10.regItem"); err != nil {
		return err
	}

	err := b.Execute(
		chromedp.SendKeys(`//*[@id="main"]/form/div/table/tbody/tr/td/div/dl[1]/dd/input`, userName),
		chromedp.SendKeys(`//*[@id="main"]/form/div/table/tbody/tr/td/div/dl[2]/dd/input`, os.Getenv("FORUM_PASSWORD")),
		chromedp.SetValue(
			`//*[@id="main"]/form/div/table/tbody/tr/td/div/dl[3]/dd/select`,
			os.Getenv("SECURITY_QUESTION"),
			chromedp.BySearch,
		),
		chromedp.SendKeys(`//*[@id="main"]/form/div/table/tbody/tr/td/div/dl[4]/dd/input`, os.Getenv("SECURITY_ANSWER")),
		chromedp.Click(`//*[@id="main"]/form/div/table/tbody/tr/td/div/dl[7]/dd/input`),
		// 登录后等待页面切换，等待 header 中出现“退出”
		chromedp.WaitVisible(`div.header_up_sign`, chromedp.ByQuery),
		// 小等待确保登录后的 cookie 已经同步
		chromedp.Sleep(2*time.Second),
	)
	if err != nil {
		log.Printf("登陆操作出错：%v", err)
		return err
	}

	return nil
}

// saveCookies 登陆后保存cookies到
func (b *Browser) SaveCookies() string {
	// 使用写入模式打开，并清空原文件内容
	file, err := os.OpenFile("./cookies", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.ModePerm)
	if err != nil {
		log.Printf("打开cookies文件失败: %v", err)
		return ""
	}
	defer file.Close()

	err = b.Execute(
		// 登录后等待页面切换，等待 header 中出现“退出”
		chromedp.WaitVisible(`div.header_up_sign`, chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			cookies, err := network.GetCookies().Do(ctx)
			if err != nil {
				return err
			}

			j, err := json.Marshal(cookies)
			if err != nil {
				return err
			}

			// 写入 JSON 数据到文件
			_, err = file.Write(j)
			return err
		}),
	)
	if err != nil {
		log.Fatal("cookies保存失败: ", err)
	}

	return file.Name()
}

// setCookies 读取Cookies文件并自动登录
func (b *Browser) SetCookies() error {
	var text string
	return b.Execute(
		chromedp.ActionFunc(func(ctx context.Context) error {
			file, err := os.Open("./cookies")
			if err != nil {
				return err
			}

			defer file.Close()

			// 读取文件数据
			jsonBlob, err := io.ReadAll(file)
			if err != nil {
				return err
			}

			var cookies []*network.CookieParam
			// Json解码
			err = json.Unmarshal(jsonBlob, &cookies)
			if err != nil {
				return err
			}
			err = network.SetCookies(cookies).Do(ctx)
			if err != nil {
				return err
			}
			return nil
		}),
		chromedp.Reload(),
		chromedp.Title(&text),
	)
}

// replyPost 回帖
func (b *Browser) ReplyPost() (string, error) {
	// 等待回帖区域加载
	if err := b.WaitForElement(ThreadTextAreaSelector); err != nil {
		log.Printf("等待回帖区域加载失败: %v", err)
		return "", err
	}

	// 随机选择回帖内容
	replyContent := ReplyContents[time.Now().Unix()%int64(len(ReplyContents))]
	// 输入回帖内容
	if err := b.Input(ThreadTextAreaSelector, replyContent); err != nil {
		log.Printf("输入回帖内容失败: %v", err)
		return "", err
	}

	// 点击回帖按钮
	if err := b.Click(".btn.fpbtn"); err != nil {
		log.Printf("点击回帖按钮失败: %v", err)
		return "", err
	}
	// 等待3秒，刷新页面
	time.Sleep(3 * time.Second)
	return replyContent, nil
}

// 到签到页面签到
func (b *Browser) CheckIn() (string, error) {
	// 直接导航到签到页面
	if err := b.NavigateTo(BaseURL + CheckInSection); err != nil {
		return "", err
	}
	// 等待签到按钮加载
	if err := b.WaitForElement("#submit_bbb"); err != nil {
		return "", err
	}
	// 随机选择一个表情
	expressions := []string{"kx", "ng", "ym", "wl", "nu", "ch", "fd", "yl", "shuai"}
	selected := expressions[rand.IntN(len(expressions))]
	// 获取签到结果文本
	var resultText string
	// 执行选择表情与点击签到按钮的操作
	err := b.Execute(
		// 点击选中的表情对应的 radio 按钮
		chromedp.Click(`input[name="qdxq"][value="`+selected+`"]`, chromedp.ByQuery),
		// 点击签到按钮（根据 index.html，其 id 为 submit_bbb）
		chromedp.Click(`#submit_bbb`, chromedp.ByQuery),
		// 等待签到结果文本加载
		chromedp.Text("span.f14", &resultText, chromedp.ByQuery),
	)
	if err != nil {
		log.Printf("签到操作出错：%v", err)
		return "", err
	}
	log.Printf("%s 签到结果：%s", time.Now().Format("2006-01-02"), resultText)
	return resultText, nil
}

// GetUserInfo 获取用户信息
func (b *Browser) GetUserInfo() (string, error) {
	// 直接导航到用户信息页面
	if err := b.NavigateTo(BaseURL + UserInfoSection); err != nil {
		return "", err
	}

	time.Sleep(5 * time.Second)

	// 获取用户信息区域的HTML
	infoHTML, err := b.GetHTML(`.pwB_uConside_a`)
	if err != nil {
		log.Printf("获取用户信息区域HTML失败: %v", err)
		return "", err
	}

	// 使用goquery解析HTML
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(infoHTML))
	if err != nil {
		log.Printf("解析用户信息HTML失败: %v", err)
		return "", err
	}

	// 创建一个结构保存用户信息
	userInfo := make(map[string]string)

	// 遍历表格行，提取需要的四个信息
	doc.Find("table.pwB_uTable_a tr").Each(func(i int, s *goquery.Selection) {
		key := strings.TrimSpace(s.Find("td").First().Text())
		value := strings.TrimSpace(s.Find("th").First().Text())

		// 只保存指定的四个信息
		if key == "威望" || key == "金币" || key == "貢獻值" || key == "邀請幣" {
			userInfo[key] = value
		}
	})

	// 将收集到的信息格式化为文本
	var sb strings.Builder
	sb.WriteString("📊 用户积分信息 📊\n")

	// 按特定顺序添加关键信息
	keyInfo := []string{"威望", "金币", "貢獻值", "邀請幣"}

	for _, key := range keyInfo {
		if value, ok := userInfo[key]; ok {
			sb.WriteString(fmt.Sprintf("📌 %s: %s\n", key, value))
		}
	}

	// 如果没有找到任何信息
	if len(userInfo) == 0 {
		return "无法获取用户积分信息", nil
	}

	log.Printf("成功获取用户积分信息: %+v", userInfo)
	return sb.String(), nil
}

// sendTelegramNotification 发送 Telegram 消息通知
func SendTelegramNotification(message string) error {
	bot, err := tgbotapi.NewBotAPI(MyBotToken)
	if err != nil {
		log.Printf("创建 Telegram Bot 实例失败: %v", err)
		return err
	}
	bot.Debug = false

	// 构建发送消息对象
	msg := tgbotapi.NewMessage(ChatID, message)
	_, err = bot.Send(msg)
	if err != nil {
		log.Printf("发送 Telegram 消息通知失败: %v", err)
		return err
	}
	return nil
}

func finishNewManTask() {
	// 创建浏览器实例
	browser, err := NewBrowser()
	if err != nil {
		log.Printf("创建浏览器实例失败: %v", err)
		scheduleRetry("创建浏览器失败: " + err.Error())
		return
	}

	// 确保无论如何浏览器都会被关闭
	browserClosed := false
	defer func() {
		if !browserClosed {
			log.Println("关闭浏览器实例...")
			browser.Close()
		}
	}()

	// 1. 访问论坛回帖页面
	replyURL := BaseURL + ReplySection
	if err = browser.NavigateTo(replyURL); err != nil {
		log.Printf("导航回帖页失败: %v", err)
		scheduleRetry("导航回帖页失败: " + err.Error())
		return
	}

	// 2. 检查登陆状态
	if err = browser.CheckLoginStatus(); err != nil {
		log.Printf("检查登陆状态出错：%v", err)
		scheduleRetry("检查登陆状态出错: " + err.Error())
		return
	}

	pre := 8
	for i := 0; i < 30; i++ {
		if err = browser.NavigateTo(replyURL); err != nil {
			log.Printf("导航回帖页失败: %v", err)
			scheduleRetry("导航回帖页失败: " + err.Error())
			return
		}

		// 3. 获取第一个符合条件的帖子数据
		newPre, postTitle, href, err := browser.GetIndexedPost(pre)
		if err != nil {
			log.Printf("提取数据失败: %v", err)
			scheduleRetry("提取数据失败: " + err.Error())
			return
		}
		if newPre == pre {
			log.Printf("未找到新帖子，已遍历所有帖子")
			break
		}
		pre = newPre

		// 4. 打开帖子
		fullURL := BaseURL + href
		if err = browser.NavigateTo(fullURL); err != nil {
			log.Printf("打开帖子失败: %v", err)
			scheduleRetry("打开帖子失败: " + err.Error())
			return
		}

		// 5. 回帖
		replyContent, err := browser.ReplyPost()
		if err != nil {
			log.Printf("回帖失败: %v", err)
			scheduleRetry("回帖失败: " + err.Error())
			return
		}
		log.Printf("成功回复帖子: \n标题：%s, \n回帖：%s", postTitle, replyContent)
		ns := rand.IntN(5) + 15
		time.Sleep(time.Duration(ns) * time.Second)
	}
}

func main() {
	log.Println("程序启动...")
	if userName == "" {
		log.Println("未配置用户名...")
		os.Exit(1)
	}
	log.Println("签到用户名: " + userName)

	// 初始化签到状态变量
	todayCheckInSuccess = false
	lastCheckInDate = time.Now().Format("2006-01-02")

	// 启动Chrome进程监控
	monitorStop := make(chan struct{})
	go func() {
		monitorChromeProcesses(monitorStop)
	}()

	// 如果配置了立即执行任务，则立即执行一次
	if RunOnStart {
		if os.Getenv("RUN_MODE") == "finishNewManTask" {
			time.Sleep(1 * time.Hour)
			users := strings.Split(userName, ",")
			for _, u := range users {
				userName = u
				finishNewManTask()
			}
		} else {
			executeTask()
		}
	}

	// 停止监控
	close(monitorStop)

	// 停止重试计时器
	if retryTimer != nil {
		retryTimer.Stop()
	}

	// 清理Chrome进程
	killPreviousChrome()

	log.Println("程序已安全退出")
}
