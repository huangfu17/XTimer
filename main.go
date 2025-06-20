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
	"image"
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
	bgImage          *canvas.Image
	content          *fyne.Container
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

type MyApp struct {
}

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
	StatColorText  string  `json:"statColorText"`
	workPathText   *widget.Label
	//breakPathText   *widget.Label
	bgPathText *widget.Label
}

var noteColor color.Color = color.RGBA{R: 126, G: 165, B: 106, A: 255}
var statColor color.Color = color.RGBA{R: 126, G: 165, B: 106, A: 255}
var breakColor color.Color = color.RGBA{R: 126, G: 165, B: 106, A: 255}
var workColor color.Color = color.RGBA{R: 223, G: 93, B: 31, A: 255}

func main() {
	myApp := app.NewWithID("XTimer")

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

	//pomodoro.bgImage = canvas.NewImageFromFile(pomodoro.setting.BgImgPath)
	//pomodoro.bgImage = createTransparentImage(pomodoro.setting.BgImgPath, 0)
	//overlay := canvas.NewRectangle(color.NRGBA{R: 229, G: 234, B: 197, A: 200})
	//overlay.Resize(pomodoro.window.Canvas().Size())
	//pomodoro.bgImage.FillMode = canvas.ImageFillStretch
	content = container.NewPadded(createUI())

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

func createTransparentImage(imgPath string, alpha uint8) *canvas.Image {
	file, err := os.Open(imgPath)
	if err != nil {
		return canvas.NewImageFromResource(theme.FyneLogo())
	}
	defer file.Close()

	// 解码图像
	srcImg, _, err := image.Decode(file)
	if err != nil {
		return canvas.NewImageFromResource(theme.FyneLogo())
	}

	bounds := srcImg.Bounds()

	transparentImg := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))

	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			origColor := srcImg.At(x, y)
			r, g, b, _ := origColor.RGBA()
			newColor := color.NRGBA{
				R: uint8(r >> 8),
				G: uint8(g >> 8),
				B: uint8(b >> 8),
				A: alpha, // 设置透明度
			}
			transparentImg.Set(x, y, newColor)
		}
	}
	return canvas.NewImageFromImage(transparentImg)
}

func loadResource(path string) (fyne.Resource, error) {
	data, err := assets.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return fyne.NewStaticResource(filepath.Base(path), data), nil
}

// 打开设置窗口的方法
func showSettingsWindow() {
	// 如果设置窗口已存在，则将其置于最前
	if settingsWindow != nil {
		settingsWindow.RequestFocus()
		return
	}

	// 创建新的设置窗口
	settingsWindow = myApp.NewWindow("设置")
	settingsWindow.SetCloseIntercept(func() {
		settingsWindow.Hide() // 隐藏而不是关闭，以便保留状态
	})

	// 设置窗口大小
	settingsWindow.Resize(fyne.NewSize(500, 500))

	// 创建设置内容
	settingsContent := createSettingsContent()

	// 创建标题栏（用于拖动）
	//titleBar := p.createSettingsTitleBar()

	// 创建按钮区域
	buttonArea := container.NewHBox(
		layout.NewSpacer(),
		widget.NewButton("取消", func() {
			settingsWindow.Hide()
		}),
		widget.NewButton("保存", func() {
			saveSettings()
			settingsWindow.Hide()
			window.Resize(fyne.NewSize(setting.Width, setting.Height))
		}),
		widget.NewButton("保存并应用", func() {
			saveSettings()
			window.Resize(fyne.NewSize(setting.Width, setting.Height))
			// 可以添加其他应用设置的操作
		}),
	)

	// 完整布局
	content := container.NewBorder(
		//titleBar,   // 顶部标题栏
		buttonArea, // 底部按钮
		nil, nil,
		container.NewVScroll(settingsContent), // 可滚动的设置内容
	)

	settingsWindow.SetContent(content)
	settingsWindow.Show()
}

// 创建设置窗口的标题栏（可拖动）
//func (p *MyApp) createSettingsTitleBar() fyne.CanvasObject {
//	titleLabel := widget.NewLabel("设置")
//	titleLabel.TextStyle.Bold = true
//
//	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
//		if p.settingsWindow != nil {
//			p.settingsWindow.Hide()
//		}
//	})
//	closeButton.Importance = widget.LowImportance
//
//	titleBar := container.NewHBox(
//		titleLabel,
//		layout.NewSpacer(),
//		closeButton,
//	)
//
//	// 添加半透明背景
//	titleBg := canvas.NewRectangle(color.NRGBA{R: 240, G: 240, B: 240, A: 230})
//	titleContainer := container.NewMax(titleBg, titleBar)
//
//	// 实现拖动功能
//	var dragStart fyne.Position
//	titleContainer.OnTapped = func(event *fyne.PointEvent) {
//		dragStart = event.Position
//	}
//	titleContainer.AddListener(&fyne.DragListener{
//		Dragged: func(event *fyne.DragEvent) {
//			if p.settingsWindow == nil {
//				return
//			}
//			deltaX := event.PointEvent.Position.X - dragStart.X
//			deltaY := event.PointEvent.Position.Y - dragStart.Y
//			pos := p.settingsWindow.Position()
//			p.settingsWindow.Move(fyne.NewPos(pos.X+deltaX, pos.Y+deltaY))
//		},
//	})
//
//	return titleContainer
//}

func createUI() fyne.CanvasObject {
	toolbar := widget.NewToolbar(
		widget.NewToolbarAction(theme.SettingsIcon(), func() {
			//p.showSettingsWindow()
			setting.Width = window.Canvas().Size().Width
			setting.Height = window.Canvas().Size().Height
			window.Resize(fyne.NewSize(500, 400))
			settingsDialog := dialog.NewCustomConfirm(
				"设置",
				"保存",
				"取消",
				createSettingsContent(),
				func(confirmed bool) {
					if confirmed {
						saveSettings()
					}
					window.Resize(fyne.NewSize(setting.Width, setting.Height))
				},
				window,
			)
			settingsDialog.Resize(fyne.NewSize(500, 400))
			settingsDialog.Show()
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
	if currentState == stateWorking {
		title = "工作完成了！"
		message = "辛苦了，休息一会吧！"
		nextState = stateBreaking
		soundFile = setting.WorkInformPath
		updatePomodoro()
		saveTaskRecord()
		checkAndRefreshToday()
	} else {
		title = "继续工作了！"
		message = "休息结束，要工作了，加油！"
		nextState = stateWorking
		soundFile = setting.WorkInformPath
	}

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
		ensureFocus()
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

func createSettingsContent() fyne.CanvasObject {
	var formItems []*widget.FormItem

	workEntry := newFixedWidthEntry(100, 36)
	workEntry.Objects[0].(*widget.Entry).SetText(strconv.Itoa(setting.WorkTime))
	workEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		if val, err := strconv.Atoi(text); err == nil {
			setting.WorkTime = val
		}
	}
	workContainer := container.NewHBox(workEntry, widget.NewLabel("分钟"))
	formItems = append(formItems, widget.NewFormItem("番茄时钟:", workContainer))

	breakEntry := newFixedWidthEntry(100, 36)
	breakEntry.Objects[0].(*widget.Entry).SetText(strconv.Itoa(setting.BreakTime))
	breakEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		if val, err := strconv.Atoi(text); err == nil {
			setting.BreakTime = val
		}
	}
	breakContainer := container.NewHBox(breakEntry, widget.NewLabel("分钟"))
	formItems = append(formItems, widget.NewFormItem("休息时钟:", breakContainer))

	workColorEntry := newFixedWidthEntry(100, 36)
	workColorEntry.Objects[0].(*widget.Entry).SetText(setting.WorkColorText)
	workColorEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		workColor, _ = hexToColor(text)
		setting.WorkColorText = text
		if currentState == stateWorking {
			timeText.Color = workColor
		}
	}
	formItems = append(formItems, widget.NewFormItem("番茄钟色:", workColorEntry))

	breakColorEntry := newFixedWidthEntry(100, 36)
	breakColorEntry.Objects[0].(*widget.Entry).SetText(setting.BreakColorText)
	breakColorEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		breakColor, _ = hexToColor(text)
		setting.BreakColorText = text
		if currentState != stateWorking {
			timeText.Color = breakColor
		}
	}
	formItems = append(formItems, widget.NewFormItem("休息钟色:", breakColorEntry))

	NoteColorEntry := newFixedWidthEntry(100, 36)
	NoteColorEntry.Objects[0].(*widget.Entry).SetText(setting.NoteColorText)
	NoteColorEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		setting.NoteColorText = text
		noteColor, _ = hexToColor(text)
		stateText.Color = noteColor
	}
	formItems = append(formItems, widget.NewFormItem("状态字色:", NoteColorEntry))

	statColorEntry := newFixedWidthEntry(100, 36)
	statColorEntry.Objects[0].(*widget.Entry).SetText(setting.StatColorText)
	statColorEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		setting.StatColorText = text
		statColor, _ = hexToColor(text)
		statTimeText.Color = statColor
		statCountText.Color = statColor
	}
	formItems = append(formItems, widget.NewFormItem("统计字色:", statColorEntry))

	// 上课铃设置
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

	// 添加垂直间距
	return container.NewVBox(form)
}

//func createSettingsContent2() fyne.CanvasObject {
//	workEntry := widget.NewEntry()
//	workEntry.SetText(strconv.Itoa(setting.WorkTime))
//	workEntry.OnChanged = func(text string) {
//		if val, err := strconv.Atoi(text); err == nil {
//			setting.WorkTime = val
//		}
//	}
//
//	breakEntry := widget.NewEntry()
//	breakEntry.SetText(strconv.Itoa(setting.BreakTime))
//	breakEntry.OnChanged = func(text string) {
//		if val, err := strconv.Atoi(text); err == nil {
//			setting.BreakTime = val
//		}
//	}
//
//
//	setting.workPathText = widget.NewLabel("未设置")
//	if setting.WorkInformPath != "" {
//		setting.workPathText.SetText(truncatePath(setting.WorkInformPath, 30))
//	}
//	selectWorkInformBtn := widget.NewButton("更改", selectWorkFile)
//
//	setting.breakPathText = widget.NewLabel("未设置")
//	if setting.BreakInformPath != "" {
//		setting.breakPathText.SetText(truncatePath(setting.BreakInformPath, 30))
//	}
//	selectBreakInformBtn := widget.NewButton("更改", selectBreakFile)
//
//	return container.NewVBox(
//		container.NewHBox(widget.NewLabel("番茄钟:"), workEntry, widget.NewLabel("分钟")),
//		layout.NewSpacer(),
//		container.NewHBox(widget.NewLabel("休息钟:"), breakEntry, widget.NewLabel("分钟")),
//		layout.NewSpacer(),
//		container.NewHBox(widget.NewLabel("背景图:"), setting.bgPathText, selectBgImgBtn),
//		layout.NewSpacer(),
//		container.NewHBox(widget.NewLabel("上课铃:"), setting.workPathText, selectWorkInformBtn),
//		layout.NewSpacer(),
//		container.NewHBox(widget.NewLabel("下课铃:"), setting.breakPathText, selectBreakInformBtn),
//	)
//}

func selectWorkFile() {
	selectFile(func(filePath string) {
		setting.WorkInformPath = filePath
		setting.workPathText.SetText(truncatePath(filePath, 30))
	}, "mp3")
}

//func selectBgImgFile() {
//	selectFile(func(filePath string) {
//		setting.BgImgPath = filePath
//		setting.bgPathText.SetText(truncatePath(filePath, 30))
//		content.Objects[0] = canvas.NewImageFromFile(setting.BgImgPath)
//		content.Refresh()
//	}, "img")
//}

//func selectBreakFile() {
//	selectFile(func(filePath string) {
//		setting.BreakInformPath = filePath
//		setting.breakPathText.SetText(truncatePath(filePath, 30))
//	}, "mp3")
//}

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

func ensureFocus() {
	// 延迟请求焦点
	go func() {
		// 第一次延迟
		time.Sleep(200 * time.Millisecond)
		window.RequestFocus()

		// 第二次延迟（增加成功率）
		time.Sleep(500 * time.Millisecond)
		window.RequestFocus()

		// 第三次延迟（针对特别顽固的情况）
		time.Sleep(1000 * time.Millisecond)
		window.RequestFocus()
	}()
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
