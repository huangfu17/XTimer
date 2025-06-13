package main

import "C"
import (
	"database/sql"
	"encoding/json"
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	_ "github.com/mattn/go-sqlite3"
	"gopkg.in/natefinch/lumberjack.v2"
	"image/color"
	"io"
	"io/ioutil"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	workDuration   = 10 * time.Second
	breakDuration  = 10 * time.Second
	timerPrecision = time.Second
)

type state int

const (
	stateIdle state = iota
	stateWorking
	stateBreaking
	statePaused
)

type MyApp struct {
	app              fyne.App
	window           fyne.Window
	timer            *time.Timer
	remaining        time.Duration
	total            time.Duration
	startTime        time.Time
	pauseStartTime   time.Time
	totalRunningTime time.Duration
	isRunning        bool
	timeText         *canvas.Text
	stateText        *canvas.Text
	statImage        *canvas.Image
	statTimeText     *canvas.Text
	statCountText    *canvas.Text
	startBtn         *widget.Button
	resetBtn         *widget.Button
	setting          *settings
	state            state
	lastState        state
	logger           *Logger
	notifying        bool
	pomodoroCount    int
	pomodoroTime     int
	today            string
}

var (
	clockImage    fyne.Resource
	pomodoroImage fyne.Resource
	workingImage  fyne.Resource
	breakingImage fyne.Resource
	pauseImage    fyne.Resource
	pauseIcon     fyne.Resource
	playIcon      fyne.Resource
	stopIcon      fyne.Resource
)

// TODO error handle
func (p *MyApp) initResources() error {

	clockPng, err := ioutil.ReadFile("clock.png")
	if err != nil {
		p.logError(err, "reload clock png failed.")
	}
	clockImage = fyne.NewStaticResource("pause_icon", clockPng)

	pomodoroPng, err := ioutil.ReadFile("pomodoro.png")
	if err != nil {
		p.logError(err, "reload pomodoro png failed.")
	}
	pomodoroImage = fyne.NewStaticResource("pause_icon", pomodoroPng)

	workingPng, err := ioutil.ReadFile("working.png")
	if err != nil {
		p.logError(err, "reload working png failed.")
	}
	workingImage = fyne.NewStaticResource("working_icon", workingPng)

	breakingPng, err := ioutil.ReadFile("breaking.png")
	if err != nil {
		p.logError(err, "reload breaking png failed.")
	}
	breakingImage = fyne.NewStaticResource("breaking_icon", breakingPng)

	pausePng, err := ioutil.ReadFile("pause.png")
	if err != nil {
		p.logError(err, "reload pause png failed.")
	}
	pauseImage = fyne.NewStaticResource("pause_icon", pausePng)

	pauseIcon = theme.Icon(theme.IconNameMediaPause)
	playIcon = theme.Icon(theme.IconNameMediaPlay)
	stopIcon = theme.Icon(theme.IconNameMediaStop)
	return nil
}

func (p *MyApp) saveTaskRecord() {

	record := TaskRecord{
		Date:      p.startTime.Format("2006-01-02"),
		StartTime: p.startTime,
		EndTime:   time.Now(),
		Duration:  int(math.Ceil(p.total.Minutes())),
		Type:      "pomodoro",
	}
	p.logInfo("task record=", record)

	go func() {
		if err := addTimeRecord(record); err != nil {
			p.logError("insert task record error, type id", record.Type)
		}
	}()
}

func main() {

	//init
	myApp := app.NewWithID("xTimer")
	pomodoro := &MyApp{
		app:       myApp,
		state:     stateIdle,
		remaining: workDuration,
		total:     workDuration,
		logger:    newDefaultLogger(),
	}
	pomodoro.loadSettings()
	pomodoro.window = myApp.NewWindow("XTimer")
	pomodoro.window.Resize(fyne.NewSize(550, 380))
	pomodoro.window.SetFixedSize(true)

	err := pomodoro.initDatabase()
	if err != nil {
		//TODO
		return
	}

	if icon, err := fyne.LoadResourceFromPath("logo.jpeg"); err == nil {
		pomodoro.window.SetIcon(icon)
	}

	err = pomodoro.initResources()
	pomodoro.today = time.Now().Format("2006-01-02")
	pomodoro.pomodoroCount, _ = countRecordByDate(pomodoro.today)
	pomodoro.pomodoroTime, _ = getTotalWorkTimeByDate(pomodoro.today)

	//background := canvas.NewImageFromFile("back.jpg")
	//background.FillMode = canvas.ImageFillStretch // 可以选择不同的填充模式
	//background := canvas.NewRectangle(color.NRGBA{R: 0x1a, G: 0x1a, B: 0x1a, A: 0xff})
	background := canvas.NewRectangle(color.RGBA{R: 50, G: 120, B: 50, A: 80})

	// 创建一个容器，将背景放在最底层，内容放在上面
	overlay := container.NewStack(background, container.NewVBox(pomodoro.createUI()))

	//background2 := canvas.NewRectangle(color.RGBA{R: 255, G: 230, B: 230, A: 255})
	//content := container.NewWithoutLayout(background2, pomodoro.createUI())
	//overlay := container.NewVBox(pomodoro.createUI())
	pomodoro.window.SetContent(overlay)

	pomodoro.window.SetContent(overlay)
	pomodoro.window.ShowAndRun()

}

func (p *MyApp) createUI() fyne.CanvasObject {

	toolbar := widget.NewToolbar(
		widget.NewToolbarAction(theme.Icon(theme.IconNameSettings), func() {
			settingsDialog := dialog.NewCustomConfirm(
				"设置",
				"保存",
				"取消",
				p.createSettingsContent(),
				func(confirmed bool) {
					if confirmed {
						p.saveSettings()
					}
				},
				p.window,
			)
			settingsDialog.Resize(fyne.NewSize(400, 300))
			settingsDialog.Show()
		}),
	)

	p.stateText = canvas.NewText("准备开始", color.RGBA{R: 50, G: 120, B: 50, A: 255})
	p.stateText.TextSize = 32

	p.statImage = canvas.NewImageFromResource(pauseImage)
	p.statImage.FillMode = canvas.ImageFillContain
	p.statImage.SetMinSize(fyne.NewSize(40, 40))

	stateContent := container.NewCenter(
		container.NewHBox(
			container.NewVBox(p.stateText),
			p.statImage,
		),
	)

	// 创建时间显示
	p.timeText = canvas.NewText(formatDuration(p.remaining), color.RGBA{R: 180, G: 30, B: 30, A: 255})
	p.timeText.TextSize = 100

	// 创建按钮
	p.startBtn = widget.NewButtonWithIcon("开始", playIcon, p.toggleTimer)
	p.startBtn.Importance = widget.HighImportance
	p.resetBtn = widget.NewButtonWithIcon("重置", stopIcon, p.resetTimer)
	p.resetBtn.Importance = widget.DangerImportance
	p.resetBtn.Disable()

	p.statCountText = canvas.NewText(p.getPomodoroCount(), color.RGBA{R: 50, G: 120, B: 50, A: 255})
	p.statCountText.TextSize = 16

	p.statTimeText = canvas.NewText(p.getPomodoroTime(), color.RGBA{R: 50, G: 120, B: 50, A: 255})
	p.statTimeText.TextSize = 16

	countIcon := widget.NewIcon(pomodoroImage)
	timeIcon := widget.NewIcon(clockImage)

	countItem := container.NewHBox(
		countIcon,
		container.NewVBox(
			p.statCountText,
		),
	)

	timeItem := container.NewHBox(
		timeIcon,
		container.NewVBox(
			p.statTimeText,
		),
	)

	statsContainer := container.NewGridWithRows(2,
		countItem,
		timeItem,
	)

	topBox := container.NewHBox(
		toolbar,
		layout.NewSpacer(),
		statsContainer,
	)

	// 创建按钮容器
	buttonBox := container.NewHBox(
		layout.NewSpacer(),
		p.startBtn,
		layout.NewSpacer(),
		layout.NewSpacer(),
		p.resetBtn,
		layout.NewSpacer(),
	)

	content := container.NewBorder(
		topBox, // 顶部元素
		nil,
		nil, // 底部元素
		nil, // 左侧元素（无）
		container.NewVBox(
			container.NewCenter(stateContent),
			layout.NewSpacer(),
			container.NewCenter(p.timeText),
			layout.NewSpacer(),
			buttonBox,
			layout.NewSpacer(),
		),
	)

	return container.NewPadded(content)
}

func (p *MyApp) toggleTimer() {
	if !p.isRunning {
		p.startTimer()
	} else {
		p.pauseTimer()
	}
}

func (p *MyApp) startTimer() {
	p.startTime = time.Now()
	if p.state != stateIdle && !p.isRunning {
		//
	} else {
		if p.state == stateIdle {
			p.state = stateWorking
			p.total = workDuration
			p.remaining = p.total
		}

		p.totalRunningTime = 0
	}

	p.isRunning = true
	p.stateText.Text = "专注中..."
	p.statImage.Resource = workingImage
	p.statImage.Refresh()

	p.startBtn.SetText("暂停")
	p.startBtn.SetIcon(pauseIcon)
	p.startBtn.Importance = widget.WarningImportance
	p.resetBtn.Enable()

	if p.timer != nil {
		p.timer.Stop()
	}

	p.startTime = time.Now()
	p.timer = time.AfterFunc(timerPrecision, p.updateTimer)
}

func (p *MyApp) updateTimer() {
	if !p.isRunning {
		return
	}

	currentRunTime := time.Since(p.startTime)
	p.startTime = time.Now()
	p.totalRunningTime = p.totalRunningTime + currentRunTime
	p.remaining = p.total - p.totalRunningTime

	if p.remaining <= 0 {
		p.timerComplete()
		return
	}
	fyne.DoAndWait(func() {
		p.timeText.Text = formatDuration(p.remaining)
		p.timeText.Refresh()
	})
	p.timer = time.AfterFunc(timerPrecision, p.updateTimer)
}

func (p *MyApp) pauseTimer() {
	p.isRunning = false
	p.stateText.Text = "暂个停..."
	p.statImage.Resource = pauseImage
	p.statImage.Refresh()

	p.startBtn.SetText("继续")
	p.startBtn.SetIcon(playIcon)
	p.startBtn.Importance = widget.HighImportance

	if p.timer != nil {
		p.timer.Stop()
	}
}

func (p *MyApp) resetTimer() {
	p.pauseTimer()
	p.state = stateIdle
	p.remaining = workDuration
	p.timeText.Text = formatDuration(p.remaining)
	p.stateText.Text = "准备开始"
	p.statImage.Resource = pauseImage
	p.statImage.Refresh()

	p.stateText.Color = color.Black
	p.startBtn.SetText("开始")
	p.resetBtn.Disable()
}

func (p *MyApp) timerComplete() {

	p.isRunning = false
	p.pauseStartTime = time.Time{}
	p.lastState = p.state

	p.showNotification()
}

func (p *MyApp) updatePomodoro() {
	p.pomodoroTime = p.pomodoroTime + int(math.Ceil(p.total.Minutes()))
	p.pomodoroCount += 1

	p.statTimeText.Text = p.getPomodoroTime()
	p.statCountText.Text = p.getPomodoroCount()

	p.statTimeText.Refresh()
	p.statCountText.Refresh()
}

func (p *MyApp) checkAndUpdateDay() {

}

func (p *MyApp) showNotification() {
	if p.notifying {
		return
	}

	p.notifying = true

	var title, message string
	var nextState state

	if p.lastState == stateWorking {
		title = "工作完成了！"
		message = "辛苦了，休息一会吧！"
		nextState = stateBreaking
		playCustomSound("end.mp3")
		p.updatePomodoro()
		p.saveTaskRecord()
	} else {
		title = "继续工作"
		message = "休息结束，要工作了， 加油！"
		nextState = stateWorking
		playCustomSound("start.mp3")
	}

	settingsDialog := dialog.NewCustomConfirm(
		title,
		"好的",
		"就不",
		container.NewCenter(canvas.NewText(message, color.Black)),
		func(confirmed bool) {
			p.state = nextState
			p.notifying = false

			if nextState == stateBreaking {
				p.total = breakDuration
				p.stateText.Text = "休息中..."
				p.stateText.Color = color.RGBA{R: 50, G: 50, B: 180, A: 255}
				p.statImage.Resource = breakingImage
				p.statImage.Refresh()
			} else {
				p.total = workDuration
				p.stateText.Text = "工作中..."
				p.stateText.Color = color.RGBA{R: 50, G: 120, B: 50, A: 255}
				p.statImage.Resource = workingImage
				p.statImage.Refresh()
			}

			p.remaining = p.total
			p.startTime = time.Now()
			p.totalRunningTime = 0

			fyne.Do(func() {
				p.timeText.Text = formatDuration(p.remaining)
				p.timeText.Refresh()
			})

			if p.timer != nil {
				p.timer.Stop()
			}

			if confirmed {
				p.isRunning = true
				p.timer = time.AfterFunc(timerPrecision, p.updateTimer)
			}
		},
		p.window,
	)
	settingsDialog.Resize(fyne.NewSize(200, 150))
	settingsDialog.Show()

	fyne.Do(func() {
		p.window.RequestFocus()
	})
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	return fmt.Sprintf("%02d:%02d", m, s)
}

func (p *MyApp) getPomodoroCount() string {
	return fmt.Sprintf(": %d", p.pomodoroCount)
}

func (p *MyApp) getPomodoroTime() string {
	h := p.pomodoroTime / 60
	m := p.pomodoroTime % 60
	return fmt.Sprintf(": %d小时%d分", h, m)
}

type settings struct {
	WorkTime        int    `json:"workTime"`
	BreakTime       int    `json:"breakTime"`
	WorkInformPath  string `json:"workInformPath"`
	BreakInformPath string `json:"breakInformPath"`
	workPathText    *widget.Label
	breakPathText   *widget.Label
}

func (p *MyApp) loadSettings() {
	if _, err := os.Stat("settings.json"); os.IsNotExist(err) {
		p.setting = &settings{
			WorkTime:  25,
			BreakTime: 5,
		}
		return
	}

	data, err := os.ReadFile("settings.json")
	if err != nil {
		dialog.ShowError(err, p.window)
		return
	}

	err = json.Unmarshal(data, &p.setting)
	if err != nil {
		dialog.ShowError(err, p.window)
		p.setting = &settings{
			WorkTime:  25,
			BreakTime: 5,
		}
	}
	if p.setting.WorkTime == 0 {
		p.setting.WorkTime = 25
	}
	if p.setting.BreakTime == 0 {
		p.setting.BreakTime = 5
	}
}

func (p *MyApp) saveSettings() {
	jsonData, err := json.MarshalIndent(p.setting, "", "  ")
	if err != nil {
		dialog.ShowError(err, p.window)
		return
	}
	err = os.WriteFile("settings.json", jsonData, 0644)
	if err != nil {
		dialog.ShowError(err, p.window)
	}
}

func (p *MyApp) createSettingsContent() fyne.CanvasObject {

	workEntry := widget.NewEntry()
	workEntry.SetText(fmt.Sprintf("%d", p.setting.WorkTime))
	workEntry.OnChanged = func(text string) {
		if val, err := strconv.Atoi(text); err == nil {
			p.setting.WorkTime = val
		}
	}

	breakEntry := widget.NewEntry()
	breakEntry.SetText(fmt.Sprintf("%d", p.setting.BreakTime))
	breakEntry.OnChanged = func(text string) {
		if val, err := strconv.Atoi(text); err == nil {
			p.setting.BreakTime = val
		}
	}

	p.setting.workPathText = widget.NewLabel("默认")
	if p.setting.WorkInformPath != "" {
		p.setting.workPathText.SetText(truncatePath(p.setting.WorkInformPath))
	}
	selectWorkInformBtn := widget.NewButton("更改", p.selectWorkFile)

	p.setting.breakPathText = widget.NewLabel("默认")
	if p.setting.BreakInformPath != "" {
		p.setting.breakPathText.SetText(truncatePath(p.setting.BreakInformPath))
	}
	selectBreakInformBtn := widget.NewButton("更改", p.selectBreakFile)

	return container.NewVBox(
		container.NewHBox(
			widget.NewLabel("番茄钟:"),
			workEntry,
			widget.NewLabel("分钟"),
		),
		layout.NewSpacer(),
		container.NewHBox(
			widget.NewLabel("休息钟:"),
			breakEntry,
			widget.NewLabel("分钟"),
		),
		layout.NewSpacer(),
		container.NewHBox(
			widget.NewLabel("上课铃:"),
			p.setting.workPathText,
			selectWorkInformBtn,
		),
		layout.NewSpacer(),
		container.NewHBox(
			widget.NewLabel("下课铃:"),
			p.setting.breakPathText,
			selectBreakInformBtn,
		),
	)
}

func (p *MyApp) selectWorkFile() {
	dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			dialog.ShowInformation("提示", "未选择文件", p.window)
			return
		}
		defer func(reader fyne.URIReadCloser) {
			err := reader.Close()
			if err != nil {
				p.logError("close sound file error", err)
			}
		}(reader)
		filePath := reader.URI().Path()

		if !isAudioFile(filePath) {
			dialog.ShowInformation("提示", "请选择音频文件", p.window)
			return
		}

		p.setting.WorkInformPath = filePath
		fyne.Do(func() {
			p.setting.workPathText.SetText(truncatePath(filePath))
		})
	}, p.window)
}

func (p *MyApp) selectBreakFile() {
	dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			dialog.ShowInformation("提示", "未选择文件", p.window)
			return
		}
		defer func(reader fyne.URIReadCloser) {
			err := reader.Close()
			if err != nil {
				p.logError("close sound file error", err)
			}
		}(reader)
		filePath := reader.URI().Path()

		if !isAudioFile(filePath) {
			dialog.ShowInformation("提示", "请选择音频文件", p.window)
			return
		}

		p.setting.BreakInformPath = filePath
		fyne.Do(func() {
			p.setting.breakPathText.SetText(truncatePath(filePath))
		})
	}, p.window)
}

func truncatePath(path string) string {
	if len(path) <= 30 {
		return path
	}
	return "..." + path[len(path)-30:]
}

func isAudioFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	audioExts := []string{".mp3", ".wav", ".aac", ".m4a", ".ogg", ".oga", ".flac"}

	for _, audioExt := range audioExts {
		if ext == audioExt {
			return true
		}
	}
	return false
}

func createWidthSpace(width float32) fyne.CanvasObject {
	rect := canvas.NewRectangle(color.Transparent)
	rect.Resize(fyne.NewSize(width, 1))
	return container.NewWithoutLayout(rect)
}

func createHighSpace(height float32) fyne.CanvasObject {
	rect := canvas.NewRectangle(color.Transparent)
	rect.Resize(fyne.NewSize(1, height)) // 设置宽度，高度可以是任意值

	// 使用自定义布局容器
	return container.NewWithoutLayout(rect)
}

type Logger struct {
	*log.Logger
	config *LoggerConfig
}

type LoggerConfig struct {
	LogPath      string
	LogFileName  string
	MaxSize      int  // 单个日志文件最大大小(MB)
	MaxBackups   int  // 保留的旧日志文件数量
	MaxAge       int  // 日志文件保留天数
	Compress     bool // 是否压缩旧日志
	ConsolePrint bool // 是否同时输出到控制台
}

func newDefaultLogger() *Logger {

	loggerConfig := &LoggerConfig{
		LogPath:      "./logs",
		LogFileName:  "app",
		MaxSize:      20,   // 20MB
		MaxBackups:   3,    // 最多保留3个备份
		MaxAge:       7,    // 保留7天
		Compress:     true, // 压缩旧日志
		ConsolePrint: true, // 同时输出到控制台
	}

	return newLogger(loggerConfig)
}

func newLogger(config *LoggerConfig) *Logger {

	if config.MaxSize <= 0 {
		config.MaxSize = 20 // 默认20MB
	}
	if config.MaxBackups <= 0 {
		config.MaxBackups = 3 // 默认保留3个备份
	}
	if config.MaxAge <= 0 {
		config.MaxAge = 365 // 默认保留7天
	}

	if err := os.MkdirAll(config.LogPath, 0755); err != nil {
		panic(fmt.Sprintf("无法创建日志目录:path=%s, err=%v", config.LogPath, err))
	}

	logFilePath := fmt.Sprintf("%s/%s.log", config.LogPath, config.LogFileName)

	lumberjackLogger := &lumberjack.Logger{
		Filename:   logFilePath,
		MaxSize:    config.MaxSize,
		MaxBackups: config.MaxBackups,
		MaxAge:     config.MaxAge,
		Compress:   config.Compress,
	}

	logFlags := log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile

	var logger *log.Logger

	if config.ConsolePrint {
		logger = log.New(io.MultiWriter(os.Stdout, lumberjackLogger), "", logFlags)
	} else {
		logger = log.New(lumberjackLogger, "", logFlags)
	}

	return &Logger{
		Logger: logger,
		config: config,
	}
}

func (p *MyApp) logInfo(v ...any) {
	p.logger.Printf(fmt.Sprintln("[INFO]", v))
}

func (p *MyApp) logWarn(v ...any) {
	p.logger.Printf(fmt.Sprintln("[WARN]", v))
}

func (p *MyApp) logError(v ...any) {
	p.logger.Printf(fmt.Sprintln("[ERROR]", v))
}

func (p *MyApp) logErrorWithErr(err error, v ...any) {
	p.logger.Printf(fmt.Sprintln("[ERROR]", v), err)
}

type TaskRecord struct {
	ID        int       `json:"id"`
	Date      string    `json:"date"` // 格式: "2023-01-01"
	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime"`
	Duration  int       `json:"duration"` // 单位: 分钟
	Type      string    `json:"type"`
}

const CREATE_SQL = `
        CREATE TABLE IF NOT EXISTS task_record (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            date TEXT NOT NULL,
            start_time TEXT NOT NULL,
            end_time TEXT NOT NULL,
            duration INTEGER NOT NULL,
            type TEXT NOT NULL
        )
    `

const INSERT_SQL = "INSERT INTO task_record (date, start_time, end_time, duration, type) VALUES (?, ?, ?, ?, ?)"

const SELECT_SQL = "SELECT id, date, start_time, end_time, duration, type FROM task_record WHERE date = ? ORDER BY start_time"

var db *sql.DB

// 初始化数据库
func (p *MyApp) initDatabase() error {
	var err error
	db, err = sql.Open("sqlite3", "./pomodoro.db")
	if err != nil {
		p.logError(err, "open sqlLite3 failed.")
		return err
	}
	_, err = db.Exec(CREATE_SQL)
	if err != nil {
		p.logError(err, "create task_record table failed.")
	}
	return err
}

// 添加新的时间记录
func addTimeRecord(record TaskRecord) error {
	_, err := db.Exec(
		INSERT_SQL,
		record.Date,
		record.StartTime.Format(time.DateTime),
		record.EndTime.Format(time.DateTime),
		record.Duration,
		record.Type,
	)
	return err
}

// 获取某天的所有记录
func getRecordsByDate(date string) ([]TaskRecord, error) {
	rows, err := db.Query(SELECT_SQL, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []TaskRecord
	for rows.Next() {
		var record TaskRecord
		var startTimeStr, endTimeStr string

		if err := rows.Scan(
			&record.ID,
			&record.Date,
			&startTimeStr,
			&endTimeStr,
			&record.Duration,
			&record.Type,
		); err != nil {
			return nil, err
		}

		record.StartTime, _ = time.Parse(time.DateTime, startTimeStr)
		record.EndTime, _ = time.Parse(time.DateTime, endTimeStr)
		records = append(records, record)
	}

	return records, nil
}

// 获取某天的总工作时间（分钟）
func getTotalWorkTimeByDate(date string) (int, error) {
	var total int
	err := db.QueryRow(
		"SELECT SUM(duration) FROM task_record WHERE date = ? ",
		date,
	).Scan(&total)
	return total, err
}

func countRecordByDate(date string) (int, error) {
	var total int
	err := db.QueryRow(
		"select count(*) FROM task_record WHERE date = ? ", date,
	).Scan(&total)
	return total, err
}

// 获取某天的总休息时间（分钟）
func getTotalBreakTimeByDate(date string) (int, error) {
	var total int
	err := db.QueryRow(
		"SELECT SUM(duration) FROM task_record WHERE date = ? AND type = 'break'",
		date,
	).Scan(&total)
	return total, err
}

// 获取最近N天的统计数据
func getStatsForLastNDays(n int) ([]map[string]interface{}, error) {
	today := time.Now()
	var stats []map[string]interface{}

	for i := 0; i < n; i++ {
		date := today.AddDate(0, 0, -i).Format("2006-01-02")
		workTime, _ := getTotalWorkTimeByDate(date)
		breakTime, _ := getTotalBreakTimeByDate(date)

		stats = append(stats, map[string]interface{}{
			"date":      date,
			"workTime":  workTime,
			"breakTime": breakTime,
			"totalTime": workTime + breakTime,
		})
	}

	return stats, nil
}

func playCustomSound(filePath string) {
	switch runtime.GOOS {
	case "darwin":
		err := exec.Command("afplay", filePath).Start()
		if err != nil {
			//TODO 日志
			return
		}
	case "windows":
		err := exec.Command("cmd", "/c", "start", filePath).Start()
		if err != nil {
			//TODO 日志
			return
		}
	case "linux":
		err := exec.Command("aplay", filePath).Start()
		if err != nil {
			//TODO 日志
			return
		}
	}
}
