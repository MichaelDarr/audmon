package cmd

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MichaelDarr/audmon/internal"
	"github.com/gdamore/tcell/v2"
	"github.com/gen2brain/malgo"
	"github.com/rivo/tview"
)

const (
	framesPerSecond = 60
)

var (
	flagVersion bool
)

func Execute() {
	flag.Parse()

	if flagVersion {
		fmt.Printf("audmon %s\n", internal.Version)
		os.Exit(0)
	}

	// Initialize malgo (go miniaudio wrapper) context
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(message string) {
		log.Printf("malgo: %v", message)
	})
	if err != nil {
		log.Fatalf("failed to initialize malgo context: %v", err)
	}
	defer func() {
		_ = ctx.Uninit()
		ctx.Free()
	}()

	// Set up tui app
	app := tview.NewApplication()
	monitorBar := tview.NewFlex().SetDirection(tview.FlexRow)
	monitorBarFiller := tview.NewBox()
	monitorBar.
		AddItem(monitorBarFiller, 0, 1, false).
		AddItem(tview.NewBox().SetBackgroundColor(tcell.Color103), 0, 1, false)

	// Configure audio capture device
	captureDeviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	captureDeviceConfig.Capture.Format = malgo.FormatU8
	captureDeviceConfig.Alsa.NoMMap = 1
	captureDeviceConfig.PeriodSizeInMilliseconds = uint32((time.Second / framesPerSecond).Milliseconds())
	var prevVolumeDisplayed float64 = 0
	device, err := malgo.InitDevice(ctx.Context, captureDeviceConfig, malgo.DeviceCallbacks{
		Data: func(_, pSample []byte, _ uint32) {
			// Find the loudest sample within the period
			var maxSample uint8 = 0
			for _, sample := range pSample {
				if sample > maxSample {
					maxSample = sample
				}
			}

			// volume is a value between 0 (minimum) and 1 (maximum).
			// This value is scaled to loosely approximate the decibel curve.
			volume := math.Min(math.Pow(float64(maxSample)/235, 2), 1)
			// Throttle bar movement to roughly three times the length of the bar per second.
			volumeDisplayed := volume
			if volumeDisplayed > prevVolumeDisplayed {
				if volumeDisplayed-prevVolumeDisplayed > 0.05 {
					volumeDisplayed = prevVolumeDisplayed + 0.05
				}
			} else if prevVolumeDisplayed-volumeDisplayed > 0.05 {
				volumeDisplayed = prevVolumeDisplayed - 0.05
			}
			prevVolumeDisplayed = volumeDisplayed

			// Adjust the volume bar
			app.QueueUpdateDraw(func() {
				_, _, _, barHeight := monitorBar.GetInnerRect()
				monitorBar.ResizeItem(monitorBarFiller, int(math.Floor((1-volumeDisplayed)*float64(barHeight))), 0)
			})
		},
	})
	if err != nil {
		log.Fatalf("failed to initialize audio capture device: %v", err)
	}

	// Display the tui & monitor audio input
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		if err := app.SetRoot(monitorBar, true).SetFocus((monitorBar)).Run(); err != nil {
			log.Printf("closed unsuccessfully: %v", err)
		}
		done <- syscall.SIGTERM
	}()
	if err = device.Start(); err != nil {
		log.Fatalf("failed to start audio capture device: %v", err)
	}

	// Block until a shutoff signal is recieved
	<-done
	device.Uninit()
	app.Stop()
	os.Exit(0)
}

type flagInfo struct {
	fallback bool
	usage    string
}

func (f flagInfo) usageShorthand() string {
	return fmt.Sprintf("%s (shorthand)", f.usage)
}

func init() {
	flagVersionInfo := flagInfo{false, "print version information"}
	flag.BoolVar(&flagVersion, "version", flagVersionInfo.fallback, flagVersionInfo.usage)
	flag.BoolVar(&flagVersion, "v", flagVersionInfo.fallback, flagVersionInfo.usageShorthand())
}
