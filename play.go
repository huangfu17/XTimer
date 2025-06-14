package main

import (
	"fmt"
	"github.com/faiface/beep"
	"github.com/faiface/beep/mp3"
	"github.com/faiface/beep/speaker"
	"os"
	"time"
)

func playSoundWithBeep(filePath string) {
	f, err := os.Open(filePath)
	if err != nil {
		fmt.Printf("打开音频文件失败: %v\n", err)
		return
	}
	defer f.Close()

	streamer, format, err := mp3.Decode(f)
	if err != nil {
		fmt.Printf("解码音频失败: %v\n", err)
		return
	}
	defer streamer.Close()

	// 初始化扬声器
	speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/10))

	// 播放音频
	done := make(chan bool)
	speaker.Play(beep.Seq(streamer, beep.Callback(func() {
		done <- true
	})))

	// 等待播放完成
	<-done
}
