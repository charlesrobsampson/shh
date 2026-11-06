// Spike 04: menu bar title that ticks every second, with a few clickable items.
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"time"

	"fyne.io/systray"
)

func icon() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 18, 18))
	for y := 0; y < 18; y++ {
		for x := 0; x < 18; x++ {
			dx, dy := x-9, y-9
			if dx*dx+dy*dy <= 36 {
				img.Set(x, y, color.NRGBA{A: 255})
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func main() {
	systray.Run(onReady, func() {})
}

func onReady() {
	systray.SetTemplateIcon(icon(), icon())
	remaining := 2 * time.Minute
	paused := false
	level := 65

	status := systray.AddMenuItem("baseline 30%, focus 65%", "stack")
	status.Disable()
	systray.AddSeparator()
	pause := systray.AddMenuItem("Pause", "pause or resume")
	plus := systray.AddMenuItem("+5m", "add five minutes")
	minus := systray.AddMenuItem("-5m", "remove five minutes")
	stop := systray.AddMenuItem("Stop", "stop the session")
	quit := systray.AddMenuItem("Quit spike", "")

	render := func() {
		if paused {
			systray.SetTitle(fmt.Sprintf("shh ⏸ %d%%", level))
			return
		}
		m := int(remaining.Round(time.Minute).Minutes())
		if remaining < time.Minute {
			systray.SetTitle(fmt.Sprintf("shh %ds · %d%%", int(remaining.Seconds()), level))
			return
		}
		systray.SetTitle(fmt.Sprintf("shh %dm · %d%%", m, level))
	}
	render()

	tick := time.NewTicker(time.Second)
	for {
		select {
		case <-tick.C:
			if !paused && remaining > 0 {
				remaining -= time.Second
			}
			render()
		case <-pause.ClickedCh:
			paused = !paused
			if paused {
				pause.SetTitle("Resume")
			} else {
				pause.SetTitle("Pause")
			}
			fmt.Println("click: pause toggled, paused =", paused)
			render()
		case <-plus.ClickedCh:
			remaining += 5 * time.Minute
			fmt.Println("click: +5m")
			render()
		case <-minus.ClickedCh:
			remaining = max(0, remaining-5*time.Minute)
			fmt.Println("click: -5m")
			render()
		case <-stop.ClickedCh:
			remaining = 0
			level = 30
			status.SetTitle("baseline 30%")
			fmt.Println("click: stop, back to baseline")
			render()
		case <-quit.ClickedCh:
			fmt.Println("click: quit")
			systray.Quit()
			return
		}
	}
}
