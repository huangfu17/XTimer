package main

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

type state int

const (
	stateIdle state = iota
	stateWorking
	stateBreaking
)

const (
	workDuration   = 25 * time.Minute
	breakDuration  = 5 * time.Minute
	timerPrecision = time.Second
)

type MyApp struct {
	app           fyne.App
	window        fyne.Window
	ticker        *time.Ticker
	remaining     time.Duration
	total         time.Duration
	startTime     time.Time
	isRunning     bool
	timeText      *canvas.Text
	stateText     *canvas.Text
	statImage     *canvas.Image
	statTimeText  *canvas.Text
	statCountText *canvas.Text
	startBtn      *widget.Button
	resetBtn      *widget.Button
	setting       *settings
	currentState  state
	lastState     state
	logger        *Logger
	pomodoroCount int
	pomodoroTime  int
	today         string
	db            *sql.DB
}

var (
	clockImage    fyne.Resource
	pomodoroImage fyne.Resource
	workingImage  fyne.Resource
	breakingImage fyne.Resource
	pauseImage    fyne.Resource
)

const (
	CREATE_SQL = `
        CREATE TABLE IF NOT EXISTS task_record (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            date TEXT NOT NULL,
            start_time TEXT NOT NULL,
            end_time TEXT NOT NULL,
            duration INTEGER NOT NULL,
            type TEXT NOT NULL
        )
    `
	INSERT_SQL   = "INSERT INTO task_record (date, start_time, end_time, duration, type) VALUES (?, ?, ?, ?, ?)"
	SELECT_SQL   = "SELECT id, date, start_time, end_time, duration, type FROM task_record WHERE date = ? ORDER BY start_time"
	COUNT_SQL    = "select count(*) FROM task_record WHERE date = ? "
	DURATION_SQL = "SELECT SUM(duration) FROM task_record WHERE date = ? "
)

type Logger struct {
	*log.Logger
}

type LoggerConfig struct {
	LogPath      string
	LogFileName  string
	MaxSize      int
	MaxBackups   int
	MaxAge       int
	Compress     bool
	ConsolePrint bool
}

type TaskRecord struct {
	ID        int       `json:"id"`
	Date      string    `json:"date"`
	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime"`
	Duration  int       `json:"duration"`
	Type      string    `json:"type"`
}

type settings struct {
	WorkTime        int    `json:"workTime"`
	BreakTime       int    `json:"breakTime"`
	WorkInformPath  string `json:"workInformPath"`
	BreakInformPath string `json:"breakInformPath"`
	workPathText    *widget.Label
	breakPathText   *widget.Label
}

func main() {
	myApp := app.NewWithID("xTimer")
	pomodoro := &MyApp{
		app:          myApp,
		currentState: stateIdle,
		remaining:    workDuration,
		total:        workDuration,
		logger:       newDefaultLogger(),
	}

	pomodoro.window = myApp.NewWindow("XTimer")
	pomodoro.window.Resize(fyne.NewSize(550, 380))
	pomodoro.window.SetFixedSize(true)

	// 设置应用生命周期
	myApp.Lifecycle().SetOnStopped(func() {
		if pomodoro.ticker != nil {
			pomodoro.ticker.Stop()
		}
		if pomodoro.db != nil {
			pomodoro.db.Close()
		}
	})

	// 加载资源
	if icon, err := fyne.LoadResourceFromPath("logo.jpeg"); err == nil {
		pomodoro.window.SetIcon(icon)
	}
	pomodoro.loadResources()

	// 初始化数据库
	if err := pomodoro.initDatabase(); err != nil {
		dialog.ShowError(err, pomodoro.window)
		return
	}

	// 加载设置
	pomodoro.loadSettings()

	// 初始化统计
	pomodoro.today = time.Now().Format("2006-01-02")
	pomodoro.pomodoroCount, _ = pomodoro.countRecordByDate(pomodoro.today)
	pomodoro.pomodoroTime, _ = pomodoro.getTotalWorkTimeByDate(pomodoro.today)

	// 创建UI
	content := container.NewStack(
		canvas.NewRectangle(color.RGBA{R: 240, G: 248, B: 255, A: 255}),
		pomodoro.createUI(),
	)
	pomodoro.window.SetContent(content)
	pomodoro.window.ShowAndRun()
}

func (p *MyApp) loadResources() {
	var err error
	clockImage, err = fyne.LoadResourceFromPath("clock.png")
	if err != nil {
		p.logError("加载时钟图标失败:", err)
		clockImage = theme.ViewRefreshIcon()
	}

	pomodoroImage, err = fyne.LoadResourceFromPath("pomodoro.png")
	if err != nil {
		p.logError("加载番茄图标失败:", err)
		pomodoroImage = theme.InfoIcon()
	}

	workingImage, err = fyne.LoadResourceFromPath("working.png")
	if err != nil {
		p.logError("加载工作图标失败:", err)
		workingImage = theme.ComputerIcon()
	}

	breakingImage, err = fyne.LoadResourceFromPath("breaking.png")
	if err != nil {
		p.logError("加载休息图标失败:", err)
		breakingImage = theme.MediaPauseIcon()
	}

	pauseImage, err = fyne.LoadResourceFromPath("pause.png")
	if err != nil {
		p.logError("加载暂停图标失败:", err)
		pauseImage = theme.MediaPauseIcon()
	}
}

func (p *MyApp) createUI() fyne.CanvasObject {
	toolbar := widget.NewToolbar(
		widget.NewToolbarAction(theme.SettingsIcon(), func() {
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

	p.stateText = canvas.NewText("准备开始", theme.PrimaryColor())
	p.stateText.TextSize = 28

	p.statImage = canvas.NewImageFromResource(pauseImage)
	p.statImage.FillMode = canvas.ImageFillContain
	p.statImage.SetMinSize(fyne.NewSize(40, 40))

	stateContent := container.NewCenter(
		container.NewHBox(
			container.NewVBox(p.stateText),
			p.statImage,
		),
	)

	p.timeText = canvas.NewText(formatDuration(p.remaining), theme.ErrorColor())
	p.timeText.TextSize = 80

	p.startBtn = widget.NewButtonWithIcon("开始", theme.MediaPlayIcon(), p.toggleTimer)
	p.startBtn.Importance = widget.HighImportance
	p.resetBtn = widget.NewButtonWithIcon("重置", theme.MediaStopIcon(), p.resetTimer)
	p.resetBtn.Importance = widget.DangerImportance
	p.resetBtn.Disable()

	p.statCountText = canvas.NewText(p.getPomodoroCount(), theme.PrimaryColor())
	p.statCountText.TextSize = 16

	p.statTimeText = canvas.NewText(p.getPomodoroTime(), theme.PrimaryColor())
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

	statsContainer := container.NewGridWithRows(2, countItem, timeItem)

	topBox := container.NewHBox(
		toolbar,
		layout.NewSpacer(),
		statsContainer,
	)

	buttonBox := container.NewHBox(
		layout.NewSpacer(),
		p.startBtn,
		layout.NewSpacer(),
		layout.NewSpacer(),
		p.resetBtn,
		layout.NewSpacer(),
	)

	return container.NewBorder(
		topBox,
		nil,
		nil,
		nil,
		container.NewVBox(
			container.NewCenter(stateContent),
			layout.NewSpacer(),
			container.NewCenter(p.timeText),
			layout.NewSpacer(),
			buttonBox,
			layout.NewSpacer(),
		),
	)
}

func (p *MyApp) toggleTimer() {
	if !p.isRunning {
		p.startTimer()
	} else {
		p.pauseTimer()
	}
}

func (p *MyApp) startTimer() {
	if p.currentState == stateIdle {
		p.transitionState(stateWorking)
	}

	p.startTime = time.Now()
	p.isRunning = true
	p.startBtn.SetText("暂停")
	p.startBtn.SetIcon(theme.MediaPauseIcon())
	p.startBtn.Importance = widget.WarningImportance
	p.resetBtn.Enable()

	if p.ticker != nil {
		p.ticker.Stop()
	}

	p.ticker = time.NewTicker(time.Second)
	go func() {
		for range p.ticker.C {
			if !p.isRunning {
				return
			}

			elapsed := time.Since(p.startTime)
			p.remaining = p.total - elapsed

			if p.remaining <= 0 {
				p.ticker.Stop()
				p.timerComplete()
				return
			}

			fyne.Do(func() {
				p.timeText.Text = formatDuration(p.remaining)
				p.timeText.Refresh()
			})
		}
	}()
}

func (p *MyApp) pauseTimer() {
	p.isRunning = false
	p.transitionState(stateIdle)
	p.startBtn.SetText("继续")
	p.startBtn.SetIcon(theme.MediaPlayIcon())
	p.startBtn.Importance = widget.HighImportance

	if p.ticker != nil {
		p.ticker.Stop()
	}
}

func (p *MyApp) resetTimer() {
	p.pauseTimer()
	p.currentState = stateIdle
	p.remaining = workDuration
	p.timeText.Text = formatDuration(p.remaining)
	p.transitionState(stateIdle)
	p.startBtn.SetText("开始")
	p.resetBtn.Disable()
}

func (p *MyApp) timerComplete() {
	p.isRunning = false
	p.lastState = p.currentState
	p.showNotification()
}

func (p *MyApp) transitionState(newState state) {
	p.currentState = newState

	switch newState {
	case stateWorking:
		p.total = time.Duration(p.setting.WorkTime) * time.Minute
		p.stateText.Text = "专注中..."
		p.statImage.Resource = workingImage
		p.stateText.Color = theme.PrimaryColor()
	case stateBreaking:
		p.total = time.Duration(p.setting.BreakTime) * time.Minute
		p.stateText.Text = "休息中..."
		p.statImage.Resource = breakingImage
		p.stateText.Color = color.RGBA{R: 50, G: 50, B: 180, A: 255}
	case stateIdle:
		p.stateText.Text = "已暂停"
		p.statImage.Resource = pauseImage
		p.stateText.Color = theme.DisabledColor()
	}

	p.statImage.Refresh()
	p.stateText.Refresh()
}

func (p *MyApp) showNotification() {
	var title, message string
	var nextState state
	var soundFile string

	if p.lastState == stateWorking {
		title = "工作完成了！"
		message = "辛苦了，休息一会吧！"
		nextState = stateBreaking
		soundFile = "end.mp3"
		p.updatePomodoro()
		p.saveTaskRecord()
	} else {
		title = "继续工作"
		message = "休息结束，要工作了，加油！"
		nextState = stateWorking
		soundFile = "start.mp3"
	}

	// 播放声音
	go p.playSound(soundFile)

	// 显示通知
	p.app.SendNotification(fyne.NewNotification(title, message))

	dialog.ShowCustomConfirm(
		title,
		"好的",
		"取消",
		container.NewCenter(canvas.NewText(message, theme.TextColor())),
		func(confirmed bool) {
			if confirmed {
				p.transitionState(nextState)
				p.remaining = p.total
				p.startTime = time.Now()
				p.startTimer()
			}
		},
		p.window,
	)
}

func (p *MyApp) updatePomodoro() {
	p.pomodoroTime += int(math.Ceil(p.total.Minutes()))
	p.pomodoroCount++

	p.statTimeText.Text = p.getPomodoroTime()
	p.statCountText.Text = p.getPomodoroCount()

	p.statTimeText.Refresh()
	p.statCountText.Refresh()
}

func (p *MyApp) saveTaskRecord() {
	record := TaskRecord{
		Date:      p.startTime.Format("2006-01-02"),
		StartTime: p.startTime,
		EndTime:   time.Now(),
		Duration:  int(math.Ceil(p.total.Minutes())),
		Type:      "pomodoro",
	}

	if err := p.addTimeRecord(record); err != nil {
		p.logError("保存任务记录失败:", err)
	}
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

func (p *MyApp) loadSettings() {
	p.setting = &settings{
		WorkTime:  25,
		BreakTime: 5,
	}

	if _, err := os.Stat("settings.json"); os.IsNotExist(err) {
		p.saveSettings()
		return
	}

	data, err := os.ReadFile("settings.json")
	if err != nil {
		p.logError("读取设置失败:", err)
		return
	}

	if err := json.Unmarshal(data, &p.setting); err != nil {
		p.logError("解析设置失败:", err)
	}
}

func (p *MyApp) saveSettings() {
	jsonData, err := json.MarshalIndent(p.setting, "", "  ")
	if err != nil {
		p.logError("编码设置失败:", err)
		return
	}

	if err := os.WriteFile("settings.json", jsonData, 0644); err != nil {
		p.logError("保存设置失败:", err)
	}
}

func (p *MyApp) createSettingsContent() fyne.CanvasObject {
	workEntry := widget.NewEntry()
	workEntry.SetText(strconv.Itoa(p.setting.WorkTime))
	workEntry.Validator = func(text string) error {
		if _, err := strconv.Atoi(text); err != nil {
			return fmt.Errorf("请输入有效数字")
		}
		return nil
	}
	workEntry.OnChanged = func(text string) {
		if val, err := strconv.Atoi(text); err == nil {
			p.setting.WorkTime = val
		}
	}

	breakEntry := widget.NewEntry()
	breakEntry.SetText(strconv.Itoa(p.setting.BreakTime))
	breakEntry.Validator = workEntry.Validator
	breakEntry.OnChanged = func(text string) {
		if val, err := strconv.Atoi(text); err == nil {
			p.setting.BreakTime = val
		}
	}

	p.setting.workPathText = widget.NewLabel("默认")
	if p.setting.WorkInformPath != "" {
		p.setting.workPathText.SetText(truncatePath(p.setting.WorkInformPath, 30))
	}
	selectWorkInformBtn := widget.NewButton("更改", p.selectWorkFile)

	p.setting.breakPathText = widget.NewLabel("默认")
	if p.setting.BreakInformPath != "" {
		p.setting.breakPathText.SetText(truncatePath(p.setting.BreakInformPath, 30))
	}
	selectBreakInformBtn := widget.NewButton("更改", p.selectBreakFile)

	return container.NewVBox(
		container.NewHBox(widget.NewLabel("番茄钟:"), workEntry, widget.NewLabel("分钟")),
		layout.NewSpacer(),
		container.NewHBox(widget.NewLabel("休息钟:"), breakEntry, widget.NewLabel("分钟")),
		layout.NewSpacer(),
		container.NewHBox(widget.NewLabel("上课铃:"), p.setting.workPathText, selectWorkInformBtn),
		layout.NewSpacer(),
		container.NewHBox(widget.NewLabel("下课铃:"), p.setting.breakPathText, selectBreakInformBtn),
	)
}

func (p *MyApp) selectWorkFile() {
	p.selectSoundFile(func(filePath string) {
		p.setting.WorkInformPath = filePath
		p.setting.workPathText.SetText(truncatePath(filePath, 30))
	})
}

func (p *MyApp) selectBreakFile() {
	p.selectSoundFile(func(filePath string) {
		p.setting.BreakInformPath = filePath
		p.setting.breakPathText.SetText(truncatePath(filePath, 30))
	})
}

func (p *MyApp) selectSoundFile(callback func(string)) {
	dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			return
		}
		defer reader.Close()

		filePath := reader.URI().Path()
		if !isAudioFile(filePath) {
			dialog.ShowInformation("提示", "请选择音频文件 (MP3, WAV等)", p.window)
			return
		}

		callback(filePath)
	}, p.window)
}

func truncatePath(path string, maxLen int) string {
	if len(path) <= maxLen {
		return path
	}
	return "..." + path[len(path)-maxLen:]
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

func newDefaultLogger() *Logger {
	return newLogger(&LoggerConfig{
		LogPath:      "./logs",
		LogFileName:  "app",
		MaxSize:      20,
		MaxBackups:   3,
		MaxAge:       7,
		Compress:     true,
		ConsolePrint: true,
	})
}

func newLogger(config *LoggerConfig) *Logger {
	if err := os.MkdirAll(config.LogPath, 0755); err != nil {
		panic(fmt.Sprintf("无法创建日志目录: %s", config.LogPath))
	}

	logFilePath := fmt.Sprintf("%s/%s.log", config.LogPath, config.LogFileName)
	lumberjackLogger := &lumberjack.Logger{
		Filename:   logFilePath,
		MaxSize:    config.MaxSize,
		MaxBackups: config.MaxBackups,
		MaxAge:     config.MaxAge,
		Compress:   config.Compress,
	}

	var writer io.Writer
	if config.ConsolePrint {
		writer = io.MultiWriter(os.Stdout, lumberjackLogger)
	} else {
		writer = lumberjackLogger
	}

	return &Logger{
		Logger: log.New(writer, "", log.Ldate|log.Ltime|log.Lshortfile),
	}
}

func (p *MyApp) logError(message string, err error) {
	p.logger.Printf("[ERROR] %s: %v", message, err)
}

func (p *MyApp) logInfo(message string, args ...interface{}) {
	p.logger.Printf("[INFO] "+message, args...)
}

func (p *MyApp) initDatabase() error {
	var err error
	p.db, err = sql.Open("sqlite3", "./pomodoro.db")
	if err != nil {
		return fmt.Errorf("打开数据库失败: %w", err)
	}

	if _, err := p.db.Exec(CREATE_SQL); err != nil {
		return fmt.Errorf("创建表失败: %w", err)
	}
	return nil
}

func (p *MyApp) addTimeRecord(record TaskRecord) error {
	_, err := p.db.Exec(
		INSERT_SQL,
		record.Date,
		record.StartTime.Format(time.DateTime),
		record.EndTime.Format(time.DateTime),
		record.Duration,
		record.Type,
	)
	return err
}

func (p *MyApp) countRecordByDate(date string) (int, error) {
	var total int
	err := p.db.QueryRow(COUNT_SQL, date).Scan(&total)
	return total, err
}

func (p *MyApp) getTotalWorkTimeByDate(date string) (int, error) {
	var total int
	err := p.db.QueryRow(DURATION_SQL, date).Scan(&total)
	return total, err
}

func (p *MyApp) playSound(filePath string) {
	if filePath == "" {
		// 使用默认声音
		filePath = "default.mp3"
	}

	switch runtime.GOOS {
	case "darwin":
		exec.Command("afplay", filePath).Start()
	case "windows":
		exec.Command("cmd", "/c", "start", filePath).Start()
	case "linux":
		exec.Command("aplay", filePath).Start()
	}
}
