package main

import (
	"database/sql"
	"embed"
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
	"github.com/faiface/beep"
	"github.com/faiface/beep/mp3"
	"github.com/faiface/beep/speaker"
	_ "github.com/mattn/go-sqlite3"
	"gopkg.in/natefinch/lumberjack.v2"
	"image/color"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

//go:embed assets/*
var assets embed.FS

type state int

const (
	stateIdle state = iota
	stateWorking
	stateBreaking
	statePause
)

const (
	defaultEmpty = ""
)

var (
	logoImage     fyne.Resource
	clockImage    fyne.Resource
	pomodoroImage fyne.Resource
	workingImage  fyne.Resource
	breakingImage fyne.Resource
	pauseImage    fyne.Resource
)

func initResources() {

	logoImage, _ = loadResource("assets/Logo2.jpeg")
	clockImage, _ = loadResource("assets/Clock.png")
	pomodoroImage, _ = loadResource("assets/Pomodoro.png")
	workingImage, _ = loadResource("assets/Working.png")
	breakingImage, _ = loadResource("assets/Breaking.png")
	pauseImage, _ = loadResource("assets/Pause.png")
}

var (
	myApp            fyne.App
	window           fyne.Window
	settingsWindow   fyne.Window
	ticker           *time.Ticker
	remaining        time.Duration
	total            time.Duration
	totalRunningTime time.Duration
	startTime        time.Time
	lastStartTime    time.Time
	isRunning        bool
	timeText         *canvas.Text
	stateText        *canvas.Text
	statImage        *canvas.Image
	statTimeText     *canvas.Text
	statCountText    *canvas.Text
	content          *fyne.Container
	overlay          *canvas.Rectangle
	doBar            *widget.Toolbar
	doBarAction      *widget.ToolbarAction
	resetBar         *widget.Toolbar
	setting          *settings
	currentState     state
	nextState        state
	logger           *Logger
	pomodoroCount    int
	pomodoroTime     int
	today            string
	db               *sql.DB
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

type loggerConfig struct {
	LogPath      string
	LogFileName  string
	MaxSize      int
	MaxBackups   int
	MaxAge       int
	Compress     bool
	ConsolePrint bool
}

type taskRecord struct {
	ID        int       `json:"id"`
	Date      string    `json:"date"`
	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime"`
	Duration  int       `json:"duration"`
	Type      string    `json:"type"`
}

type settings struct {
	WorkTime       int    `json:"workTime"`
	BreakTime      int    `json:"breakTime"`
	WorkInformPath string `json:"workInformPath"`
	//BreakInformPath string  `json:"breakInformPath"`
	Height         float32 `json:"height"`
	Width          float32 `json:"width"`
	WorkColorText  string  `json:"workColorText"`
	BreakColorText string  `json:"breakColorText"`
	NoteColorText  string  `json:"noteColorText"`
	BgColorText    string  `json:"BgColorText"`
	StatColorText  string  `json:"statColorText"`
	workPathText   *widget.Label
	//breakPathText   *widget.Label
	bgPathText *widget.Label
}

var defaultBgColor color.Color = color.RGBA{R: 255, G: 255, B: 255, A: 255}
var defaultNoteColor color.Color = color.RGBA{R: 126, G: 165, B: 106, A: 255}
var defaultStatColor color.Color = color.RGBA{R: 126, G: 165, B: 106, A: 255}
var defaultBreakColor color.Color = color.RGBA{R: 126, G: 165, B: 106, A: 255}
var defaultWorkColor color.Color = color.RGBA{R: 223, G: 93, B: 31, A: 255}

var bgColor color.Color = defaultBgColor
var noteColor color.Color = defaultNoteColor
var statColor color.Color = defaultStatColor
var breakColor color.Color = defaultBreakColor
var workColor color.Color = defaultWorkColor

func main() {

	logger = newDefaultLogger()

	myApp = app.NewWithID("XTimer")

	window = myApp.NewWindow("XTimer")

	myApp.Lifecycle().SetOnStopped(func() {
		if ticker != nil {
			ticker.Stop()
		}
		if db != nil {
			err := db.Close()
			if err != nil {
				//mlogError("close db error", err)
			}
		}
	})

	initResources()
	loadSettings()

	if setting.WorkColorText != "" {
		toColor, err := hexToColor(setting.WorkColorText)
		if err != nil {
			workColor = toColor
		}
	}

	if setting.BreakColorText != "" {
		toColor, err := hexToColor(setting.BreakColorText)
		if err != nil {
			breakColor = toColor
		}
	}

	if setting.NoteColorText != "" {
		toColor, err := hexToColor(setting.NoteColorText)
		if err != nil {
			noteColor = toColor
		}
	}

	if setting.StatColorText != "" {
		toColor, err := hexToColor(setting.StatColorText)
		if err != nil {
			statColor = toColor
		}
	}

	if err := initDatabase(); err != nil {
		logError("init database error,", err)
		dialog.ShowError(err, window)
		return
	}

	today = time.Now().Format("2006-01-02")
	pomodoroCount, _ = countRecordByDate(today)
	pomodoroTime, _ = getTotalWorkTimeByDate(today)

	overlay = canvas.NewRectangle(bgColor)
	content = container.NewStack(overlay, createUI())

	window.SetCloseIntercept(func() {
		setting.Width = window.Canvas().Size().Width
		setting.Height = window.Canvas().Size().Height
		saveSettings()
		closeDialog := dialog.NewCustomConfirm(
			"关闭确认",
			"关闭",
			"取消",
			container.NewCenter(canvas.NewText("确定要关闭应用吗？", theme.TextColor())), func(confirmed bool) {
				if confirmed {
					window.Close()
					settingsWindow.Close()
				}
			}, window)
		closeDialog.Resize(fyne.NewSize(200, 150))
		closeDialog.Show()
	})

	resetTimer()
	window.SetIcon(logoImage)
	window.Resize(fyne.NewSize(setting.Width, setting.Height))
	window.SetPadded(false)
	window.SetContent(content)
	window.ShowAndRun()

}

func loadResource(path string) (fyne.Resource, error) {
	data, err := assets.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return fyne.NewStaticResource(filepath.Base(path), data), nil
}

func showSettingsWindow() {

	settingsWindow = myApp.NewWindow("设置")
	settingsWindow.SetCloseIntercept(func() {
		saveSettings()
		updateTimeColor()
		settingsWindow.Close()
	})

	settingsWindow.Resize(fyne.NewSize(500, 400))

	settingsContent := createSettingsContent()

	// 完整布局
	content := container.NewVScroll(settingsContent)

	settingsWindow.SetContent(content)
	settingsWindow.Show()
}

func createUI() fyne.CanvasObject {
	toolbar := widget.NewToolbar(
		widget.NewToolbarAction(theme.SettingsIcon(), func() {
			showSettingsWindow()
		}),
	)

	doBarAction = widget.NewToolbarAction(theme.MediaPlayIcon(), toggleTimer)
	doBar = widget.NewToolbar(doBarAction)
	resetBar = widget.NewToolbar(widget.NewToolbarAction(theme.MediaStopIcon(),
		func() {
			informDialog := dialog.NewCustomConfirm("确认重置", "确定", "手滑",
				container.NewCenter(canvas.NewText("重置将会清除当前状态和进度，确认吗？", workColor)), func(confirmed bool) {
					if confirmed {
						resetTimer()
					}
				}, window)
			informDialog.Resize(fyne.NewSize(300, 250))
			informDialog.Show()
		}))

	stateText = canvas.NewText("准备开始", noteColor)
	stateText.TextSize = 24

	statImage = canvas.NewImageFromResource(pauseImage)
	statImage.FillMode = canvas.ImageFillContain
	statImage.SetMinSize(fyne.NewSize(32, 32))

	stateContent := container.NewCenter(
		container.NewHBox(
			container.NewVBox(stateText),
			statImage,
		),
	)

	total = time.Duration(setting.WorkTime) * time.Minute
	remaining = total

	timeText = canvas.NewText(formatDuration(remaining), workColor)
	timeText.TextSize = 120

	statCountText = canvas.NewText(getPomodoroCount(), statColor)
	statCountText.TextSize = 20

	statTimeText = canvas.NewText(getPomodoroTime(), statColor)
	statTimeText.TextSize = 20

	countIcon := widget.NewIcon(pomodoroImage)
	timeIcon := widget.NewIcon(clockImage)

	countItem := container.NewHBox(
		countIcon,
		container.NewVBox(
			statCountText,
		),
	)

	timeItem := container.NewHBox(
		timeIcon,
		container.NewVBox(
			statTimeText,
		),
	)

	statsContainer := container.NewVBox(countItem, timeItem)
	statsContainer = container.NewPadded(statsContainer)

	barContainer := container.NewVBox(toolbar, resetBar, doBar)
	barContainer = container.NewPadded(barContainer)

	finalLayout := container.New(newProportionalLayout(0.15, 0.7, 0.15, 110, 200, 112),
		barContainer,
		container.NewCenter(
			container.NewVBox(
				container.NewCenter(stateContent),
				NewNegativeSpacer(-25),
				container.NewCenter(timeText),
			),
		),
		statsContainer,
	)

	return finalLayout
}

func toggleTimer() {
	if !isRunning {
		startTimer()
	} else {
		pauseTimer()
	}
}

func startTimer() {
	if currentState == stateIdle {
		if nextState == stateWorking {
			total = time.Duration(setting.WorkTime) * time.Minute
			remaining = total
			totalRunningTime = 0
			startTime = time.Now()
		}
		if nextState == stateBreaking {
			total = time.Duration(setting.WorkTime) * time.Minute
			remaining = total
			totalRunningTime = 0
			startTime = time.Now()
		}
	}
	lastStartTime = time.Now()
	isRunning = true
	transitionState(nextState)
	doBarAction.SetIcon(theme.MediaPauseIcon())

	if ticker == nil {
		ticker = time.NewTicker(1 * time.Second)
	} else {
		ticker.Reset(1 * time.Second)
	}

	go func() {
		for range ticker.C {
			if !isRunning {
				return
			}

			currentRunTime := time.Since(lastStartTime)
			lastStartTime = time.Now()
			totalRunningTime = totalRunningTime + currentRunTime
			remaining = total - totalRunningTime

			if remaining <= 0 {
				ticker.Stop()
				timerComplete()
				return
			}
			newText := formatDuration(remaining)
			fyne.Do(func() {
				updateTimeText(newText)
			})
		}
	}()
}

func updateTimeText(text string) {
	timeText.Text = text
	timeText.Refresh()
	runtime.GC()
}

func pauseTimer() {
	isRunning = false
	transitionState(statePause)
	doBarAction.SetIcon(theme.MediaPlayIcon())

	if ticker != nil {
		ticker.Stop()
	}
}

func resetTimer() {
	pauseTimer()
	transitionState(stateIdle)
	nextState = stateWorking
	total = time.Duration(setting.WorkTime) * time.Minute
	remaining = total
	timeText.Text = formatDuration(remaining)
	timeText.Color = workColor
	doBarAction.SetIcon(theme.MediaPlayIcon())
}

func timerComplete() {
	isRunning = false
	showNotification()
}

func transitionState(newState state) {
	checkAndRefreshToday()
	currentState = newState
	switch newState {
	case stateWorking:
		total = time.Duration(setting.WorkTime) * time.Minute
		stateText.Text = "专注中..."
		statImage.Resource = workingImage
		stateText.Color = noteColor
		timeText.Color = workColor
	case stateBreaking:
		total = time.Duration(setting.BreakTime) * time.Minute
		stateText.Text = "休息中..."
		statImage.Resource = breakingImage
		stateText.Color = noteColor
		timeText.Color = breakColor
	case stateIdle:
		stateText.Text = "准备开始"
		statImage.Resource = pauseImage
		stateText.Color = noteColor
	case statePause:
		stateText.Text = "暂个停..."
		statImage.Resource = pauseImage
		stateText.Color = noteColor
	}

	statImage.Refresh()
	stateText.Refresh()
}

func showNotification() {
	var title, message string
	var soundFile string
	newText := formatDuration(remaining)
	if currentState == stateWorking {
		title = "工作完成了！"
		message = "辛苦了，休息一会吧！"
		nextState = stateBreaking
		soundFile = setting.WorkInformPath
		updatePomodoro()
		saveTaskRecord()
		checkAndRefreshToday()
		newText = formatDuration(time.Duration(setting.BreakTime) * time.Minute)
	} else {
		title = "继续工作了！"
		message = "休息结束，要工作了，加油！"
		nextState = stateWorking
		soundFile = setting.WorkInformPath
		newText = formatDuration(time.Duration(setting.WorkTime) * time.Minute)
	}

	fyne.Do(func() {
		updateTimeText(newText)
	})

	go playSound(soundFile)

	currentState = stateIdle
	informDialog := dialog.NewCustomConfirm(
		title,
		"好的",
		"就不",
		container.NewCenter(canvas.NewText(message, theme.TextColor())),
		func(confirmed bool) {
			if confirmed {
				startTimer()
			} else {
				resetTimer()
			}
		},
		window,
	)

	informDialog.Resize(fyne.NewSize(200, 150))
	informDialog.Show()

	fyne.Do(func() {
		window.RequestFocus()

		time.Sleep(1000 * time.Millisecond)
		window.RequestFocus()
	})
}

func updatePomodoro() {
	pomodoroTime += int(math.Ceil(total.Minutes()))
	pomodoroCount++

	fyne.Do(func() {
		statTimeText.Text = getPomodoroTime()
		statCountText.Text = getPomodoroCount()

		statTimeText.Refresh()
		statCountText.Refresh()
	})
}

func checkAndRefreshToday() {
	currentDay := time.Now().Format("2006-01-02")
	if today != currentDay {
		today = currentDay
		pomodoroCount, _ = countRecordByDate(today)
		pomodoroTime, _ = countRecordByDate(today)
		fyne.Do(func() {
			statTimeText.Text = getPomodoroTime()
			statCountText.Text = getPomodoroCount()

			statTimeText.Refresh()
			statCountText.Refresh()
		})
		logInfo("day refresh. today=", today)
	}
}

func saveTaskRecord() {
	record := taskRecord{
		Date:      startTime.Format("2006-01-02"),
		StartTime: startTime,
		EndTime:   time.Now(),
		Duration:  int(math.Ceil(total.Minutes())),
		Type:      "pomodoro",
	}
	if err := addTimeRecord(record); err != nil {
		logInfo("insert task record error.", record, err)
	}
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	return fmt.Sprintf("%02d:%02d", m, s)
}

func getPomodoroCount() string {
	return fmt.Sprintf(": %d个", pomodoroCount)
}

func getPomodoroTime() string {
	return fmt.Sprintf(": %d分", pomodoroTime)
}

func loadSettings() {

	setting = &settings{
		WorkTime:       45,
		BreakTime:      15,
		WorkInformPath: defaultEmpty,
		WorkColorText:  colorToHex(workColor),
		BreakColorText: colorToHex(breakColor),
		NoteColorText:  colorToHex(noteColor),
		StatColorText:  colorToHex(statColor),
		BgColorText:    colorToHex(bgColor),
		Width:          430,
		Height:         238,
	}

	if _, err := os.Stat("settings.json"); os.IsNotExist(err) {
		logError("配置文件打开失败:", err)
		return
	}

	data, err := os.ReadFile("settings.json")
	if err != nil {
		logError("读取设置失败:", err)
		return
	}

	if err := json.Unmarshal(data, &setting); err != nil {
		logError("解析设置失败:", err)
	}

	if setting.WorkTime == 0 {
		setting.WorkTime = 45
	}
	if setting.BreakTime == 0 {
		setting.BreakTime = 15
	}
	if setting.StatColorText == "" {
		setting.StatColorText = colorToHex(statColor)
	}
	if setting.WorkColorText == "" {
		setting.WorkColorText = colorToHex(workColor)
	}
	if setting.NoteColorText == "" {
		setting.NoteColorText = colorToHex(noteColor)
	}
	if setting.BgColorText == "" {
		setting.BgColorText = colorToHex(bgColor)
	}

}

func saveSettings() {
	jsonData, err := json.MarshalIndent(setting, "", "  ")
	if err != nil {
		logError("编码设置失败:", err)
		return
	}

	if err := os.WriteFile("settings.json", jsonData, 0644); err != nil {
		logError("保存设置失败:", err)
	}
}

func updateTimeColor() {
	if currentState == stateWorking || currentState == stateIdle || (currentState == statePause && nextState == stateWorking) {
		if timeText.Color != workColor {
			timeText.Color = workColor
			timeText.Refresh()
		}
	} else {
		if timeText.Color != breakColor {
			timeText.Color = breakColor
			timeText.Refresh()
		}
	}
}

func createSettingsContent() fyne.CanvasObject {
	var formItems []*widget.FormItem

	// 工作时间设置
	workEntry := newFixedWidthEntry(100, 36)
	workEntry.Objects[0].(*widget.Entry).SetText(strconv.Itoa(setting.WorkTime))
	workEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		if val, err := strconv.Atoi(text); err == nil {
			setting.WorkTime = val
		}
		if currentState == stateIdle {
			resetTimer()
		}
	}
	workContainer := container.NewHBox(workEntry, widget.NewLabel("分钟"))
	formItems = append(formItems, widget.NewFormItem("番茄时钟:", workContainer))

	// 休息时间设置
	breakEntry := newFixedWidthEntry(100, 36)
	breakEntry.Objects[0].(*widget.Entry).SetText(strconv.Itoa(setting.BreakTime))
	breakEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		if val, err := strconv.Atoi(text); err == nil {
			setting.BreakTime = val
		}
	}
	breakContainer := container.NewHBox(breakEntry, widget.NewLabel("分钟"))
	formItems = append(formItems, widget.NewFormItem("休息时钟:", breakContainer))

	//背景色设置
	bgColorEntry := newFixedWidthEntry(100, 36)
	bgColorEntry.Objects[0].(*widget.Entry).SetText(setting.BgColorText)
	bgColorEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		if toColor, err := hexToColor(text); err == nil {
			bgColor = toColor
			setting.BgColorText = text
			overlay.FillColor = toColor
			overlay.Refresh()
		}
	}
	resetBgColorBtn := widget.NewButton("重置", func() {
		bgColor = defaultBgColor
		setting.BgColorText = colorToHex(defaultBgColor)
		bgColorEntry.Objects[0].(*widget.Entry).SetText(setting.BgColorText)
		overlay.FillColor = defaultBgColor
		overlay.Refresh()
	})
	resetBgColorContainer := container.NewHBox(
		bgColorEntry,
		layout.NewSpacer(),
		resetBgColorBtn,
	)
	formItems = append(formItems, widget.NewFormItem("背景底色:", resetBgColorContainer))

	// 番茄钟颜色设置
	workColorEntry := newFixedWidthEntry(100, 36)
	workColorEntry.Objects[0].(*widget.Entry).SetText(setting.WorkColorText)
	workColorEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		if toColor, err := hexToColor(text); err == nil {
			workColor = toColor
			setting.WorkColorText = text
			timeText.Color = workColor
			timeText.Refresh()
		}
	}
	resetWorkColorBtn := widget.NewButton("重置", func() {
		workColor = defaultWorkColor
		setting.WorkColorText = colorToHex(defaultWorkColor)
		timeText.Color = workColor
		timeText.Refresh()
		workColorEntry.Objects[0].(*widget.Entry).SetText(setting.WorkColorText)
	})
	resetWorkColorContainer := container.NewHBox(
		workColorEntry,
		layout.NewSpacer(),
		resetWorkColorBtn,
	)
	formItems = append(formItems, widget.NewFormItem("番茄钟色:", resetWorkColorContainer))

	// 休息钟颜色设置
	breakColorEntry := newFixedWidthEntry(100, 36)
	breakColorEntry.Objects[0].(*widget.Entry).SetText(setting.BreakColorText)
	breakColorEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		if toColor, err := hexToColor(text); err == nil {
			breakColor = toColor
			setting.BreakColorText = text
			timeText.Color = breakColor
			timeText.Refresh()
		}
	}
	resetBreakColorBtn := widget.NewButton("重置", func() {
		breakColor = defaultBreakColor
		setting.BreakColorText = colorToHex(defaultBreakColor)
		timeText.Color = breakColor
		timeText.Refresh()
		breakColorEntry.Objects[0].(*widget.Entry).SetText(setting.BreakColorText)
	})
	resetBreakColorContainer := container.NewHBox(
		breakColorEntry,
		layout.NewSpacer(),
		resetBreakColorBtn,
	)
	formItems = append(formItems, widget.NewFormItem("休息钟色:", resetBreakColorContainer))

	// 状态文字颜色设置
	NoteColorEntry := newFixedWidthEntry(100, 36)
	NoteColorEntry.Objects[0].(*widget.Entry).SetText(setting.NoteColorText)
	NoteColorEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		if toColor, err := hexToColor(text); err == nil {
			setting.NoteColorText = text
			noteColor = toColor
			stateText.Color = noteColor
			stateText.Refresh()
		}
	}
	resetNoteColorBtn := widget.NewButton("重置", func() {
		noteColor = defaultNoteColor
		setting.NoteColorText = colorToHex(defaultStatColor)
		stateText.Color = noteColor
		stateText.Refresh()
		NoteColorEntry.Objects[0].(*widget.Entry).SetText(setting.NoteColorText)
	})
	resetNoteColorContainer := container.NewHBox(
		NoteColorEntry,
		layout.NewSpacer(),
		resetNoteColorBtn,
	)
	formItems = append(formItems, widget.NewFormItem("状态字色:", resetNoteColorContainer))

	// 统计文字颜色设置
	statColorEntry := newFixedWidthEntry(100, 36)
	statColorEntry.Objects[0].(*widget.Entry).SetText(setting.StatColorText)
	statColorEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		if toColor, err := hexToColor(text); err == nil {
			setting.StatColorText = text
			statColor = toColor
			statTimeText.Color = statColor
			statCountText.Color = statColor
			statTimeText.Refresh()
			statCountText.Refresh()
		}
	}
	resetStatColorBtn := widget.NewButton("重置", func() {
		statColor = defaultStatColor
		setting.StatColorText = colorToHex(defaultStatColor)
		statTimeText.Color = statColor
		statCountText.Color = statColor
		statColorEntry.Objects[0].(*widget.Entry).SetText(setting.StatColorText)
		statTimeText.Refresh()
		statCountText.Refresh()
	})
	resetStatColorContainer := container.NewHBox(
		statColorEntry,
		layout.NewSpacer(),
		resetStatColorBtn,
	)
	formItems = append(formItems, widget.NewFormItem("统计字色:", resetStatColorContainer))

	// 通知铃声设置
	setting.workPathText = widget.NewLabel("未设置")
	if setting.WorkInformPath != "" {
		setting.workPathText.SetText(truncatePath(setting.WorkInformPath, 50))
	}
	selectWorkInformBtn := widget.NewButton("更改", selectWorkFile)
	workSoundContainer := container.NewHBox(
		setting.workPathText,
		layout.NewSpacer(),
		selectWorkInformBtn,
	)
	formItems = append(formItems, widget.NewFormItem("通知铃声:", workSoundContainer))

	// 创建表单
	form := widget.NewForm(formItems...)

	// 创建按钮区域
	saveButton := widget.NewButton("关闭", func() {
		saveSettings()
		updateTimeColor()
		settingsWindow.Close()
	})

	buttonArea := container.NewHBox(
		saveButton,
	)

	// 添加垂直间距和按钮区域
	return container.NewVBox(
		form,
		container.NewCenter(
			container.NewPadded(buttonArea),
		),
	)
}

func selectWorkFile() {
	selectFile(func(filePath string) {
		setting.WorkInformPath = filePath
		setting.workPathText.SetText(truncatePath(filePath, 30))
	}, "mp3")
}

func selectFile(callback func(string), fType string) {
	dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			return
		}
		defer func(reader fyne.URIReadCloser) {
			err := reader.Close()
			if err != nil {
				logError("close select sound file error", err)
			}
		}(reader)

		filePath := reader.URI().Path()
		if fType == "img" {
			if !isImgFile(filePath) {
				dialog.ShowInformation("提示", "请选择正确的图片文件)", window)
				return
			}
		} else {
			if !isAudioFile(filePath) {
				dialog.ShowInformation("提示", "请选择MP3音频文件)", window)
				return
			}
		}
		callback(filePath)
	}, window)
}

func truncatePath(path string, maxLen int) string {
	if len(path) <= maxLen {
		return path
	}
	return "..." + path[len(path)-maxLen:]
}

func isAudioFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	audioExts := []string{".mp3"}
	for _, audioExt := range audioExts {
		if ext == audioExt {
			return true
		}
	}
	return false
}

func isImgFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	audioExts := []string{".png", ".svg", ".jpg", ".jpeg", ".gif"}
	for _, audioExt := range audioExts {
		if ext == audioExt {
			return true
		}
	}
	return false
}

func newDefaultLogger() *Logger {
	return newLogger(&loggerConfig{
		LogPath:      "./logs",
		LogFileName:  "app",
		MaxSize:      20,
		MaxBackups:   3,
		MaxAge:       7,
		Compress:     true,
		ConsolePrint: true,
	})
}

func newLogger(config *loggerConfig) *Logger {
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

func logError(message string, err error) {
	logger.Printf("[ERROR] %s: %v", message, err)
}

func logInfo(message string, args ...interface{}) {
	logger.Printf("[INFO] "+message, args...)
}

func initDatabase() error {
	var err error
	db, err = sql.Open("sqlite3", "./pomodoro.db")
	if err != nil {
		logError("open db error", err)
		return fmt.Errorf("打开数据库失败: %w", err)
	}

	if _, err := db.Exec(CREATE_SQL); err != nil {
		logError("create db table error", err)
		return fmt.Errorf("创建表失败: %w", err)
	}
	return nil
}

func addTimeRecord(record taskRecord) error {
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

func countRecordByDate(date string) (int, error) {
	var total int
	err := db.QueryRow(COUNT_SQL, date).Scan(&total)
	return total, err
}

func getTotalWorkTimeByDate(date string) (int, error) {
	var total int
	err := db.QueryRow(DURATION_SQL, date).Scan(&total)
	return total, err
}

func playSound(filePath string) {
	if filePath == "" {
		return
	}
	playSoundWithBeep(filePath)
}

func playSoundWithBeep(filePath string) {
	// 打开文件
	f, err := os.Open(filePath)
	if err != nil {
		logError("open mp3 file error", err)
		return
	}

	// 解码MP3文件
	streamer, format, err := mp3.Decode(f)
	if err != nil {
		f.Close() // 解码失败时关闭文件
		logError("decode mp3 file error", err)
		return
	}

	// 初始化扬声器
	err = speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/5))
	if err != nil {
		streamer.Close() // 初始化失败时关闭流
		f.Close()        // 初始化失败时关闭文件
		logError("init speaker error", err)
		return
	}

	// 创建完成通道
	done := make(chan bool)

	// 播放音频并在完成后关闭资源
	speaker.Play(beep.Seq(streamer, beep.Callback(func() {
		// 音频播放完成后关闭流和文件
		streamer.Close()
		f.Close()
		done <- true
	})))

	// 等待播放完成
	<-done
}

type ProportionalLayout struct {
	leftRatio   float32 // 左侧区域比例
	centerRatio float32 // 中间区域比例
	rightRatio  float32 // 右侧区域比例
	leftMin     float32 // 左侧最小宽度
	centerMin   float32 // 中间最小宽度
	rightMin    float32 // 右侧最小宽度
}

func newProportionalLayout(left, center, right, leftMin, centerMin, rightMin float32) *ProportionalLayout {
	return &ProportionalLayout{
		leftRatio:   left,
		centerRatio: center,
		rightRatio:  right,
		leftMin:     leftMin,
		centerMin:   centerMin,
		rightMin:    rightMin,
	}
}

func (p *ProportionalLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) != 3 {
		return // 必须有三个对象：左、中、右
	}

	totalRatio := p.leftRatio + p.centerRatio + p.rightRatio

	leftWidth := size.Width * p.leftRatio / totalRatio
	centerWidth := size.Width * p.centerRatio / totalRatio
	rightWidth := size.Width * p.rightRatio / totalRatio

	leftDelta := float32(0)
	if leftWidth < p.leftMin {
		leftDelta = p.leftMin - leftWidth
		leftWidth = p.leftMin
	}

	centerDelta := float32(0)
	if centerWidth < p.centerMin {
		centerDelta = p.centerMin - centerWidth
		centerWidth = p.centerMin
	}

	rightDelta := float32(0)
	if rightWidth < p.rightMin {
		rightDelta = p.rightMin - rightWidth
		rightWidth = p.rightMin
	}

	totalDelta := leftDelta + centerDelta + rightDelta

	if totalDelta > 0 {
		remaining := size.Width - p.leftMin - p.centerMin - p.rightMin
		centerWidth = p.centerMin + remaining
	}

	objects[0].Resize(fyne.NewSize(leftWidth, size.Height))
	objects[0].Move(fyne.NewPos(0, 0))

	objects[1].Resize(fyne.NewSize(centerWidth, size.Height))
	objects[1].Move(fyne.NewPos(leftWidth, 0))

	objects[2].Resize(fyne.NewSize(rightWidth, size.Height))
	objects[2].Move(fyne.NewPos(leftWidth+centerWidth, 0))
}

func (p *ProportionalLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) != 3 {
		return fyne.NewSize(0, 0)
	}

	minHeight := fyne.Max(
		objects[0].MinSize().Height,
		fyne.Max(
			objects[1].MinSize().Height,
			objects[2].MinSize().Height,
		),
	)

	return fyne.NewSize(p.leftMin+p.centerMin+p.rightMin, minHeight)
}

// HexToColor 将16进制颜色字符串转换为color.Color
func hexToColor(hex string) (color.Color, error) {
	hex = strings.TrimPrefix(hex, "#")
	hex = strings.TrimPrefix(hex, "0x")

	if len(hex) == 3 {
		hex = string(hex[0]) + string(hex[0]) +
			string(hex[1]) + string(hex[1]) +
			string(hex[2]) + string(hex[2])
	}

	if len(hex) != 6 && len(hex) != 8 {
		return color.Transparent, fmt.Errorf("invalid hex color format: %s", hex)
	}

	var r, g, b, a uint8 = 0, 0, 0, 255
	var err error

	if r, err = parseHexByte(hex[0:2]); err != nil {
		return color.Transparent, err
	}
	if g, err = parseHexByte(hex[2:4]); err != nil {
		return color.Transparent, err
	}
	if b, err = parseHexByte(hex[4:6]); err != nil {
		return color.Transparent, err
	}

	if len(hex) == 8 {
		if a, err = parseHexByte(hex[6:8]); err != nil {
			return color.Transparent, err
		}
	}

	return color.NRGBA{R: r, G: g, B: b, A: a}, nil
}

func parseHexByte(hex string) (uint8, error) {
	val, err := strconv.ParseUint(hex, 16, 8)
	if err != nil {
		return 0, fmt.Errorf("invalid hex byte: %s", hex)
	}
	return uint8(val), nil
}

func colorToHex(c color.Color) string {
	r, g, b, a := c.RGBA()

	r8 := uint8(r >> 8)
	g8 := uint8(g >> 8)
	b8 := uint8(b >> 8)
	a8 := uint8(a >> 8)

	if a8 != 255 {
		return fmt.Sprintf("#%02X%02X%02X%02X", r8, g8, b8, a8)
	}

	return fmt.Sprintf("#%02X%02X%02X", r8, g8, b8)
}

type fixedWidthEntryLayout struct {
	width, height float32
}

func (f *fixedWidthEntryLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	entry := objects[0]
	entry.Resize(fyne.NewSize(f.width, f.height))
	entry.Move(fyne.NewPos(0, 0))
}

func (f *fixedWidthEntryLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(f.width, f.height)
}

// 创建固定宽度Entry
func newFixedWidthEntry(width, height float32) *fyne.Container {
	entry := widget.NewEntry()
	return container.New(&fixedWidthEntryLayout{width: width, height: height}, entry)
}

type NegativeSpacer struct {
	widget.BaseWidget
	height float32
}

func NewNegativeSpacer(height float32) *NegativeSpacer {
	s := &NegativeSpacer{height: height}
	s.ExtendBaseWidget(s)
	return s
}

func (s *NegativeSpacer) CreateRenderer() fyne.WidgetRenderer {
	return &negativeSpacerRenderer{widget: s}
}

type negativeSpacerRenderer struct {
	widget *NegativeSpacer
}

func (r *negativeSpacerRenderer) Layout(size fyne.Size) {}

func (r *negativeSpacerRenderer) MinSize() fyne.Size {
	return fyne.NewSize(0, r.widget.height)
}

func (r *negativeSpacerRenderer) Refresh() {}

func (r *negativeSpacerRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{}
}

func (r *negativeSpacerRenderer) Destroy() {}
