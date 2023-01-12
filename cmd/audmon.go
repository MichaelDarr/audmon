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
	barColor                = tcell.ColorGreen
	backgroundColorClipping = tcell.ColorRed
	framesPerSecond         = 60
	// volumeClipWarningDuration indicates how long the bar will red after a clip occurs.
	volumeClipWarningDuration = time.Second * 3
)

var (
	flagHorizontal bool
	flagVersion    bool
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
	monitorBarDirection := tview.FlexRow
	if flagHorizontal {
		monitorBarDirection = tview.FlexColumn
	}
	monitorBar := tview.NewFlex().SetDirection(monitorBarDirection)
	monitorBarFiller := tview.NewBox()
	if !flagHorizontal {
		monitorBar.AddItem(monitorBarFiller, 0, 1, false)
	}
	monitorBar.AddItem(tview.NewBox().SetBackgroundColor(barColor), 0, 1, false)
	if flagHorizontal {
		monitorBar.AddItem(monitorBarFiller, 0, 1, false)
	}

	// Configure audio capture device
	captureDeviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	captureDeviceConfig.Capture.Format = malgo.FormatU8
	captureDeviceConfig.Alsa.NoMMap = 1
	captureDeviceConfig.PeriodSizeInMilliseconds = uint32((time.Second / framesPerSecond).Milliseconds())
	var prevVolumeDisplayed float64 = 0
	clippingTracker := recentClippingTracker{}
	device, err := malgo.InitDevice(ctx.Context, captureDeviceConfig, malgo.DeviceCallbacks{
		Data: func(_, pSample []byte, _ uint32) {
			// Find the loudest sample within the period
			var maxSample uint8 = 0
			for _, sample := range pSample {
				if sample > maxSample {
					maxSample = sample
				}
			}

			// When the volume clips, warn the user by changing the bar color.
			if maxSample == 240 {
				clippingTracker.IndicateClippingOccured()
			}

			// volume is a value between 0 (minimum) and 1 (maximum).
			// The observed maximum (clipping) is 240, but the docs indicate that it should be 255. This
			// is likely a configuration issue.
			// The volume range is scaled down such that all values below 120 are collapsed to 0
			// (apparent silence), as the level very rarely drops that low.
			volume := math.Max(math.Min(float64(maxSample-120)/120, 1), 0)
			// Smooth out sudden bar movement
			volumeDisplayed := volume
			if volumeDisplayed > prevVolumeDisplayed {
				extraVolume := volumeDisplayed - prevVolumeDisplayed
				if extraVolume > 0.02 {
					volumeDisplayed = prevVolumeDisplayed + extraVolume/3
				}
			} else {
				lostVolume := prevVolumeDisplayed - volumeDisplayed
				if lostVolume > 0.02 {
					volumeDisplayed = prevVolumeDisplayed - lostVolume/3
				}
			}
			prevVolumeDisplayed = volumeDisplayed

			// Update bar
			app.QueueUpdateDraw(func() {
				// Update background color to indicate whether the audio clipped recently
				curBackgroundColor := monitorBarFiller.GetBackgroundColor()
				if curBackgroundColor == backgroundColorClipping {
					if !clippingTracker.ClippedRecently {
						monitorBarFiller.SetBackgroundColor(tcell.ColorDefault)
					}
				} else if clippingTracker.ClippedRecently {
					monitorBarFiller.SetBackgroundColor(backgroundColorClipping)
				}

				// Update bar length to indicate current volume
				_, _, barWidth, barHeight := monitorBar.GetInnerRect()
				barLength := barHeight
				if flagHorizontal {
					barLength = barWidth
				}
				monitorBar.ResizeItem(monitorBarFiller, int(math.Floor((1-volumeDisplayed)*float64(barLength))), 0)
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

	flagHorizontalInfo := flagInfo{false, "orient the monitor horizontally"}
	flag.BoolVar(&flagHorizontal, "horizontal", flagHorizontalInfo.fallback, flagHorizontalInfo.usage)
	flag.BoolVar(&flagHorizontal, "h", flagHorizontalInfo.fallback, flagHorizontalInfo.usageShorthand())
}

type recentClippingTracker struct {
	ClippedRecently    bool
	cancelPendingReset func()
}

// IndicateClippingOccured is called to indicate that audio clipping has been detected.
// `recentClippingTracker.ClippedRecently` remains true for 3 seconds after clipping occurs.
func (c *recentClippingTracker) IndicateClippingOccured() {
	if c.cancelPendingReset != nil {
		c.cancelPendingReset()
	}
	c.ClippedRecently = true
	cancelled := false
	c.cancelPendingReset = func() {
		cancelled = true
	}
	go func() {
		time.Sleep(volumeClipWarningDuration)
		if !cancelled {
			c.ClippedRecently = false
		}
	}()
}
