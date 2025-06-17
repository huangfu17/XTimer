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
	defaultEmpty    = ""
	defaultImagPath = "assets/back.jpg"
)

var (
	logoImage     fyne.Resource
	clockImage    fyne.Resource
	pomodoroImage fyne.Resource
	workingImage  fyne.Resource
	breakingImage fyne.Resource
	pauseImage    fyne.Resource
)

func (p *MyApp) initResources() {

	logoImage, _ = loadResource("assets/Logo.png")
	clockImage, _ = loadResource("assets/Clock.png")
	pomodoroImage, _ = loadResource("assets/Pomodoro.png")
	workingImage, _ = loadResource("assets/Working.png")
	breakingImage, _ = loadResource("assets/Breaking.png")
	pauseImage, _ = loadResource("assets/Pause.png")
}

type MyApp struct {
	app              fyne.App
	window           fyne.Window
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
	WorkTime        int     `json:"workTime"`
	BreakTime       int     `json:"breakTime"`
	WorkInformPath  string  `json:"workInformPath"`
	BgImgPath       string  `json:"bgImgPath"`
	BreakInformPath string  `json:"breakInformPath"`
	Height          float32 `json:"height"`
	Width           float32 `json:"width"`
	WorkColorText   string  `json:"workColorText"`
	BreakColorText  string  `json:"breakColorText"`
	NoteColorText   string  `json:"noteColorText"`
	StatColorText   string  `json:"statColorText"`
	workPathText    *widget.Label
	breakPathText   *widget.Label
	bgPathText      *widget.Label
}

var noteColor color.Color = color.RGBA{R: 126, G: 165, B: 106, A: 255}
var statColor color.Color = color.RGBA{R: 126, G: 165, B: 106, A: 255}
var breakColor color.Color = color.RGBA{R: 126, G: 165, B: 106, A: 255}
var workColor color.Color = color.RGBA{R: 223, G: 93, B: 31, A: 255}

func main() {
	myApp := app.NewWithID("XTimer")
	pomodoro := &MyApp{
		app:          myApp,
		currentState: stateIdle,
		nextState:    stateWorking,
		logger:       newDefaultLogger(),
	}

	pomodoro.window = myApp.NewWindow("XTimer")

	myApp.Lifecycle().SetOnStopped(func() {
		if pomodoro.ticker != nil {
			pomodoro.ticker.Stop()
		}
		if pomodoro.db != nil {
			err := pomodoro.db.Close()
			if err != nil {
				pomodoro.logError("close db error", err)
			}
		}
	})

	pomodoro.initResources()
	pomodoro.loadSettings()

	if pomodoro.setting.WorkColorText != "" {
		toColor, err := hexToColor(pomodoro.setting.WorkColorText)
		if err != nil {
			workColor = toColor
		}
	}

	if pomodoro.setting.BreakColorText != "" {
		toColor, err := hexToColor(pomodoro.setting.BreakColorText)
		if err != nil {
			breakColor = toColor
		}
	}

	if pomodoro.setting.NoteColorText != "" {
		toColor, err := hexToColor(pomodoro.setting.NoteColorText)
		if err != nil {
			noteColor = toColor
		}
	}

	if pomodoro.setting.StatColorText != "" {
		toColor, err := hexToColor(pomodoro.setting.StatColorText)
		if err != nil {
			statColor = toColor
		}
	}

	if err := pomodoro.initDatabase(); err != nil {
		pomodoro.logError("init database error,", err)
		dialog.ShowError(err, pomodoro.window)
		return
	}

	pomodoro.today = time.Now().Format("2006-01-02")
	pomodoro.pomodoroCount, _ = pomodoro.countRecordByDate(pomodoro.today)
	pomodoro.pomodoroTime, _ = pomodoro.getTotalWorkTimeByDate(pomodoro.today)

	pomodoro.bgImage = canvas.NewImageFromFile(pomodoro.setting.BgImgPath)
	//pomodoro.bgImage = createTransparentImage(pomodoro.setting.BgImgPath, 120)
	//overlay := canvas.NewRectangle(color.NRGBA{R: 229, G: 234, B: 197, A: 200}) // 50% 透明
	//overlay.Resize(pomodoro.window.Canvas().Size())
	pomodoro.bgImage.FillMode = canvas.ImageFillStretch
	pomodoro.content = container.NewStack(pomodoro.bgImage, pomodoro.createUI())

	pomodoro.window.SetCloseIntercept(func() {
		pomodoro.setting.Width = pomodoro.window.Canvas().Size().Width
		pomodoro.setting.Height = pomodoro.window.Canvas().Size().Height
		pomodoro.saveSettings()
		closeDialog := dialog.NewCustomConfirm(
			"关闭确认",
			"关闭",
			"取消",
			container.NewCenter(canvas.NewText("确定要关闭应用吗？", theme.TextColor())), func(confirmed bool) {
				if confirmed {
					pomodoro.window.Close()
				}
			}, pomodoro.window)
		closeDialog.Resize(fyne.NewSize(200, 150))
		closeDialog.Show()
	})

	pomodoro.window.SetIcon(logoImage)
	pomodoro.window.Resize(fyne.NewSize(pomodoro.setting.Width, pomodoro.setting.Height))
	pomodoro.window.SetPadded(false)
	pomodoro.window.SetContent(pomodoro.content)
	pomodoro.window.ShowAndRun()

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

func (p *MyApp) createUI() fyne.CanvasObject {
	toolbar := widget.NewToolbar(
		widget.NewToolbarAction(theme.SettingsIcon(), func() {
			p.setting.Width = p.window.Canvas().Size().Width
			p.setting.Height = p.window.Canvas().Size().Height
			p.window.Resize(fyne.NewSize(500, 500))
			settingsDialog := dialog.NewCustomConfirm(
				"设置",
				"保存",
				"取消",
				p.createSettingsContent(),
				func(confirmed bool) {
					if confirmed {
						p.saveSettings()
					}
					p.window.Resize(fyne.NewSize(p.setting.Width, p.setting.Height))
				},
				p.window,
			)
			settingsDialog.Resize(fyne.NewSize(500, 500))
			settingsDialog.Show()
		}),
	)

	p.doBarAction = widget.NewToolbarAction(theme.MediaPlayIcon(), p.toggleTimer)
	p.doBar = widget.NewToolbar(p.doBarAction)
	p.resetBar = widget.NewToolbar(widget.NewToolbarAction(theme.MediaStopIcon(),
		func() {
			informDialog := dialog.NewCustomConfirm("确认重置", "确定", "手滑",
				container.NewCenter(canvas.NewText("重置将会清除当前状态和进度，确认吗？", workColor)), func(confirmed bool) {
					if confirmed {
						p.resetTimer()
					}
				}, p.window)
			informDialog.Resize(fyne.NewSize(300, 250))
			informDialog.Show()
		}))

	p.stateText = canvas.NewText("准备开始", noteColor)
	p.stateText.TextSize = 24

	p.statImage = canvas.NewImageFromResource(pauseImage)
	p.statImage.FillMode = canvas.ImageFillContain
	p.statImage.SetMinSize(fyne.NewSize(32, 32))

	stateContent := container.NewCenter(
		container.NewHBox(
			container.NewVBox(p.stateText),
			p.statImage,
		),
	)

	p.total = time.Duration(p.setting.WorkTime) * time.Minute
	p.remaining = p.total

	p.timeText = canvas.NewText(formatDuration(p.remaining), workColor)
	p.timeText.TextSize = 120

	p.statCountText = canvas.NewText(p.getPomodoroCount(), statColor)
	p.statCountText.TextSize = 20

	p.statTimeText = canvas.NewText(p.getPomodoroTime(), statColor)
	p.statTimeText.TextSize = 20

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

	statsContainer := container.NewVBox(countItem, timeItem)
	statsContainer = container.NewPadded(statsContainer)

	barContainer := container.NewVBox(toolbar, p.resetBar, p.doBar)
	barContainer = container.NewPadded(barContainer)

	finalLayout := container.New(newProportionalLayout(0.15, 0.7, 0.15, 110, 200, 112),
		barContainer,
		container.NewCenter(
			container.NewVBox(
				container.NewCenter(stateContent),
				container.NewCenter(p.timeText),
			),
		),
		statsContainer,
	)

	return finalLayout
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
		if p.nextState == stateWorking {
			p.total = time.Duration(p.setting.WorkTime) * time.Minute
			p.remaining = p.total
			p.totalRunningTime = 0
			p.startTime = time.Now()
		}
		if p.nextState == stateBreaking {
			p.total = time.Duration(p.setting.WorkTime) * time.Minute
			p.remaining = p.total
			p.totalRunningTime = 0
			p.startTime = time.Now()
		}
	}
	p.lastStartTime = time.Now()
	p.isRunning = true
	p.transitionState(p.nextState)
	p.doBarAction.SetIcon(theme.MediaPauseIcon())

	if p.ticker != nil {
		p.ticker.Stop()
	}

	p.ticker = time.NewTicker(500 * time.Millisecond)
	go func() {
		for range p.ticker.C {
			if !p.isRunning {
				return
			}

			currentRunTime := time.Since(p.lastStartTime)
			p.lastStartTime = time.Now()
			p.totalRunningTime = p.totalRunningTime + currentRunTime
			p.remaining = p.total - p.totalRunningTime

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
	p.transitionState(statePause)
	p.doBarAction.SetIcon(theme.MediaPlayIcon())

	if p.ticker != nil {
		p.ticker.Stop()
	}
}

func (p *MyApp) resetTimer() {
	p.pauseTimer()
	p.transitionState(stateIdle)
	p.nextState = stateWorking
	p.total = time.Duration(p.setting.WorkTime) * time.Minute
	p.remaining = p.total
	p.timeText.Text = formatDuration(p.remaining)
	p.timeText.Color = workColor
	p.doBarAction.SetIcon(theme.MediaPlayIcon())
}

func (p *MyApp) timerComplete() {
	p.isRunning = false
	p.showNotification()
}

func (p *MyApp) transitionState(newState state) {
	p.checkAndRefreshToday()
	p.currentState = newState
	switch newState {
	case stateWorking:
		p.total = time.Duration(p.setting.WorkTime) * time.Minute
		p.stateText.Text = "专注中..."
		p.statImage.Resource = workingImage
		p.stateText.Color = noteColor
		p.timeText.Color = workColor
	case stateBreaking:
		p.total = time.Duration(p.setting.BreakTime) * time.Minute
		p.stateText.Text = "休息中..."
		p.statImage.Resource = breakingImage
		p.stateText.Color = noteColor
		p.timeText.Color = breakColor
	case stateIdle:
		p.stateText.Text = "准备开始"
		p.statImage.Resource = pauseImage
		p.stateText.Color = noteColor
	case statePause:
		p.stateText.Text = "暂个停..."
		p.statImage.Resource = pauseImage
		p.stateText.Color = noteColor
	}

	p.statImage.Refresh()
	p.stateText.Refresh()
}

func (p *MyApp) showNotification() {
	var title, message string
	var soundFile string
	if p.currentState == stateWorking {
		title = "工作完成了！"
		message = "辛苦了，休息一会吧！"
		p.nextState = stateBreaking
		soundFile = p.setting.BreakInformPath
		p.updatePomodoro()
		p.saveTaskRecord()
		p.checkAndRefreshToday()
	} else {
		title = "继续工作了！"
		message = "休息结束，要工作了，加油！"
		p.nextState = stateWorking
		soundFile = p.setting.WorkInformPath
	}

	go p.playSound(soundFile)

	p.currentState = stateIdle
	informDialog := dialog.NewCustomConfirm(
		title,
		"好的",
		"就不",
		container.NewCenter(canvas.NewText(message, theme.TextColor())),
		func(confirmed bool) {
			if confirmed {
				p.startTimer()
			} else {
				p.resetTimer()
			}
		},
		p.window,
	)

	informDialog.Resize(fyne.NewSize(200, 150))
	informDialog.Show()

	fyne.Do(func() {
		p.ensureFocus()
	})
}

func (p *MyApp) updatePomodoro() {
	p.pomodoroTime += int(math.Ceil(p.total.Minutes()))
	p.pomodoroCount++

	fyne.Do(func() {
		p.statTimeText.Text = p.getPomodoroTime()
		p.statCountText.Text = p.getPomodoroCount()

		p.statTimeText.Refresh()
		p.statCountText.Refresh()
	})
}

func (p *MyApp) checkAndRefreshToday() {
	currentDay := time.Now().Format("2006-01-02")
	if p.today != currentDay {
		p.today = currentDay
		p.pomodoroCount, _ = p.countRecordByDate(p.today)
		p.pomodoroTime, _ = p.countRecordByDate(p.today)
		fyne.Do(func() {
			p.statTimeText.Text = p.getPomodoroTime()
			p.statCountText.Text = p.getPomodoroCount()

			p.statTimeText.Refresh()
			p.statCountText.Refresh()
		})
		p.logInfo("day refresh. today=", p.today)
	}
}

func (p *MyApp) saveTaskRecord() {
	record := taskRecord{
		Date:      p.startTime.Format("2006-01-02"),
		StartTime: p.startTime,
		EndTime:   time.Now(),
		Duration:  int(math.Ceil(p.total.Minutes())),
		Type:      "pomodoro",
	}
	p.logInfo("insert task record", record)
	if err := p.addTimeRecord(record); err != nil {
		p.logInfo("insert task record error.", record, err)
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
	return fmt.Sprintf(": %d个", p.pomodoroCount)
}

func (p *MyApp) getPomodoroTime() string {
	return fmt.Sprintf(": %d分", p.pomodoroTime)
}

func (p *MyApp) loadSettings() {

	p.setting = &settings{
		WorkTime:        45,
		BreakTime:       15,
		WorkInformPath:  defaultEmpty,
		BgImgPath:       defaultImagPath,
		BreakInformPath: defaultEmpty,
		WorkColorText:   colorToHex(workColor),
		BreakColorText:  colorToHex(breakColor),
		NoteColorText:   colorToHex(noteColor),
		StatColorText:   colorToHex(statColor),
		Width:           430,
		Height:          238,
	}

	if _, err := os.Stat("settings.json"); os.IsNotExist(err) {
		p.logError("配置文件打开失败:", err)
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

	if p.setting.WorkTime == 0 {
		p.setting.WorkTime = 45
	}
	if p.setting.BreakTime == 0 {
		p.setting.BreakTime = 15
	}
	if p.setting.StatColorText == "" {
		p.setting.StatColorText = colorToHex(statColor)
	}
	if p.setting.WorkColorText == "" {
		p.setting.WorkColorText = colorToHex(workColor)
	}
	if p.setting.NoteColorText == "" {
		p.setting.NoteColorText = colorToHex(noteColor)
	}
	if p.setting.BgImgPath == "" {
		p.setting.BgImgPath = defaultImagPath
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
	var formItems []*widget.FormItem

	workEntry := newFixedWidthEntry(100, 36)
	workEntry.Objects[0].(*widget.Entry).SetText(strconv.Itoa(p.setting.WorkTime))
	workEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		if val, err := strconv.Atoi(text); err == nil {
			p.setting.WorkTime = val
		}
	}
	workContainer := container.NewHBox(workEntry, widget.NewLabel("分钟"))
	formItems = append(formItems, widget.NewFormItem("番茄时钟:", workContainer))

	breakEntry := newFixedWidthEntry(100, 36)
	breakEntry.Objects[0].(*widget.Entry).SetText(strconv.Itoa(p.setting.BreakTime))
	breakEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		if val, err := strconv.Atoi(text); err == nil {
			p.setting.BreakTime = val
		}
	}
	breakContainer := container.NewHBox(breakEntry, widget.NewLabel("分钟"))
	formItems = append(formItems, widget.NewFormItem("休息时钟:", breakContainer))

	workColorEntry := newFixedWidthEntry(100, 36)
	workColorEntry.Objects[0].(*widget.Entry).SetText(p.setting.WorkColorText)
	workColorEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		workColor, _ = hexToColor(text)
		p.setting.WorkColorText = text
		if p.currentState == stateWorking {
			p.timeText.Color = workColor
		}
	}
	formItems = append(formItems, widget.NewFormItem("番茄钟色:", workColorEntry))

	breakColorEntry := newFixedWidthEntry(100, 36)
	breakColorEntry.Objects[0].(*widget.Entry).SetText(p.setting.BreakColorText)
	breakColorEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		breakColor, _ = hexToColor(text)
		p.setting.BreakColorText = text
		if p.currentState != stateWorking {
			p.timeText.Color = breakColor
		}
	}
	formItems = append(formItems, widget.NewFormItem("休息钟色:", breakColorEntry))

	NoteColorEntry := newFixedWidthEntry(100, 36)
	NoteColorEntry.Objects[0].(*widget.Entry).SetText(p.setting.NoteColorText)
	NoteColorEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		p.setting.NoteColorText = text
		noteColor, _ = hexToColor(text)
		p.stateText.Color = noteColor
	}
	formItems = append(formItems, widget.NewFormItem("状态字色:", NoteColorEntry))

	statColorEntry := newFixedWidthEntry(100, 36)
	statColorEntry.Objects[0].(*widget.Entry).SetText(p.setting.StatColorText)
	statColorEntry.Objects[0].(*widget.Entry).OnChanged = func(text string) {
		p.setting.StatColorText = text
		statColor, _ = hexToColor(text)
		p.statTimeText.Color = statColor
		p.statCountText.Color = statColor
	}
	formItems = append(formItems, widget.NewFormItem("统计字色:", statColorEntry))

	p.setting.bgPathText = widget.NewLabel(defaultImagPath)
	if p.setting.BgImgPath != "" {
		p.setting.bgPathText.SetText(truncatePath(p.setting.BgImgPath, 50))
	}
	selectBgImgBtn := widget.NewButton("更改", p.selectBgImgFile)
	BgImgContainer := container.NewHBox(
		p.setting.bgPathText,
		layout.NewSpacer(),
		selectBgImgBtn,
	)
	formItems = append(formItems, widget.NewFormItem("背景图片:", BgImgContainer))

	// 上课铃设置
	p.setting.workPathText = widget.NewLabel("未设置")
	if p.setting.WorkInformPath != "" {
		p.setting.workPathText.SetText(truncatePath(p.setting.WorkInformPath, 50))
	}
	selectWorkInformBtn := widget.NewButton("更改", p.selectWorkFile)
	workSoundContainer := container.NewHBox(
		p.setting.workPathText,
		layout.NewSpacer(),
		selectWorkInformBtn,
	)
	formItems = append(formItems, widget.NewFormItem("上课铃声:", workSoundContainer))

	// 下课铃设置
	p.setting.breakPathText = widget.NewLabel("未设置")
	if p.setting.BreakInformPath != "" {
		p.setting.breakPathText.SetText(truncatePath(p.setting.BreakInformPath, 50))
	}
	selectBreakInformBtn := widget.NewButton("更改", p.selectBreakFile)
	breakSoundContainer := container.NewHBox(
		p.setting.breakPathText,
		layout.NewSpacer(),
		selectBreakInformBtn,
	)
	formItems = append(formItems, widget.NewFormItem("下课铃声:", breakSoundContainer))

	// 创建表单
	form := widget.NewForm(formItems...)

	// 添加垂直间距
	return container.NewVBox(form)
}

func (p *MyApp) createSettingsContent2() fyne.CanvasObject {
	workEntry := widget.NewEntry()
	workEntry.SetText(strconv.Itoa(p.setting.WorkTime))
	workEntry.OnChanged = func(text string) {
		if val, err := strconv.Atoi(text); err == nil {
			p.setting.WorkTime = val
		}
	}

	breakEntry := widget.NewEntry()
	breakEntry.SetText(strconv.Itoa(p.setting.BreakTime))
	breakEntry.OnChanged = func(text string) {
		if val, err := strconv.Atoi(text); err == nil {
			p.setting.BreakTime = val
		}
	}

	p.setting.bgPathText = widget.NewLabel("未设置")
	if p.setting.BgImgPath != "" {
		p.setting.bgPathText.SetText(truncatePath(p.setting.BgImgPath, 30))
	}
	selectBgImgBtn := widget.NewButton("更改", p.selectBgImgFile)

	p.setting.workPathText = widget.NewLabel("未设置")
	if p.setting.WorkInformPath != "" {
		p.setting.workPathText.SetText(truncatePath(p.setting.WorkInformPath, 30))
	}
	selectWorkInformBtn := widget.NewButton("更改", p.selectWorkFile)

	p.setting.breakPathText = widget.NewLabel("未设置")
	if p.setting.BreakInformPath != "" {
		p.setting.breakPathText.SetText(truncatePath(p.setting.BreakInformPath, 30))
	}
	selectBreakInformBtn := widget.NewButton("更改", p.selectBreakFile)

	return container.NewVBox(
		container.NewHBox(widget.NewLabel("番茄钟:"), workEntry, widget.NewLabel("分钟")),
		layout.NewSpacer(),
		container.NewHBox(widget.NewLabel("休息钟:"), breakEntry, widget.NewLabel("分钟")),
		layout.NewSpacer(),
		container.NewHBox(widget.NewLabel("背景图:"), p.setting.bgPathText, selectBgImgBtn),
		layout.NewSpacer(),
		container.NewHBox(widget.NewLabel("上课铃:"), p.setting.workPathText, selectWorkInformBtn),
		layout.NewSpacer(),
		container.NewHBox(widget.NewLabel("下课铃:"), p.setting.breakPathText, selectBreakInformBtn),
	)
}

func (p *MyApp) selectWorkFile() {
	p.selectFile(func(filePath string) {
		p.setting.WorkInformPath = filePath
		p.setting.workPathText.SetText(truncatePath(filePath, 30))
	}, "mp3")
}

func (p *MyApp) selectBgImgFile() {
	p.selectFile(func(filePath string) {
		p.setting.BgImgPath = filePath
		p.setting.bgPathText.SetText(truncatePath(filePath, 30))
		p.content.Objects[0] = canvas.NewImageFromFile(p.setting.BgImgPath)
		p.content.Refresh()
	}, "img")
}

func (p *MyApp) selectBreakFile() {
	p.selectFile(func(filePath string) {
		p.setting.BreakInformPath = filePath
		p.setting.breakPathText.SetText(truncatePath(filePath, 30))
	}, "mp3")
}

func (p *MyApp) selectFile(callback func(string), fType string) {
	dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			return
		}
		defer func(reader fyne.URIReadCloser) {
			err := reader.Close()
			if err != nil {
				p.logError("close select sound file error", err)
			}
		}(reader)

		filePath := reader.URI().Path()
		if fType == "img" {
			if !isImgFile(filePath) {
				dialog.ShowInformation("提示", "请选择正确的图片文件)", p.window)
				return
			}
		} else {
			if !isAudioFile(filePath) {
				dialog.ShowInformation("提示", "请选择MP3音频文件)", p.window)
				return
			}
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
		p.logError("open db error", err)
		return fmt.Errorf("打开数据库失败: %w", err)
	}

	if _, err := p.db.Exec(CREATE_SQL); err != nil {
		p.logError("create db table error", err)
		return fmt.Errorf("创建表失败: %w", err)
	}
	return nil
}

func (p *MyApp) addTimeRecord(record taskRecord) error {
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

func (p *MyApp) ensureFocus() {
	// 延迟请求焦点
	go func() {
		// 第一次延迟
		time.Sleep(200 * time.Millisecond)
		p.window.RequestFocus()

		// 第二次延迟（增加成功率）
		time.Sleep(500 * time.Millisecond)
		p.window.RequestFocus()

		// 第三次延迟（针对特别顽固的情况）
		time.Sleep(1000 * time.Millisecond)
		p.window.RequestFocus()
	}()
}

func (p *MyApp) playSound(filePath string) {
	if filePath == "" {
		return
	}
	p.playSoundWithBeep(filePath)
}

func (p *MyApp) playSoundWithBeep(filePath string) {

	f, err := os.Open(filePath)
	if err != nil {
		p.logError("open mp3 file error", err)
		return
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			p.logError("close mp3 file error", err)
		}
	}(f)

	streamer, format, err := mp3.Decode(f)
	if err != nil {
		p.logError("decode mp3 file error", err)
		return
	}
	defer func(streamer beep.StreamSeekCloser) {
		err := streamer.Close()
		if err != nil {
			p.logError("close mp3 stream file error", err)
		}
	}(streamer)

	err = speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/5))
	if err != nil {
		p.logError("init speaker error", err)
		return
	}

	done := make(chan bool)
	speaker.Play(beep.Seq(streamer, beep.Callback(func() {
		done <- true
	})))

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
