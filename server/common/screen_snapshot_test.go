package common

import (
	"sync"
	"testing"
)

func TestScreenSnapshotRemainsStable(t *testing.T) {
	previousFPS := GetScreen().FPS
	defer SetScreen("fps", previousFPS)
	SetScreen("fps", 30)
	before := GetScreen()
	SetScreen("fps", 60)
	if before.FPS != 30 {
		t.Fatalf("published snapshot changed to %d", before.FPS)
	}
	if GetScreen().FPS != 60 {
		t.Fatal("new value was not published")
	}
}

func TestConcurrentScreenUpdates(t *testing.T) {
	previous := *GetScreen()
	defer func() {
		SetScreen("resolution", int(previous.Height))
		SetScreen("fps", previous.FPS)
	}()
	SetScreen("resolution", 720)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				if worker%2 == 0 {
					if i%2 == 0 {
						SetScreen("resolution", 1080)
					} else {
						SetScreen("resolution", 720)
					}
					SetScreen("fps", 30+i%2*30)
				} else {
					current := GetScreen()
					if ResolutionMap[current.Height] != current.Width {
						t.Errorf("torn resolution: %dx%d", current.Width, current.Height)
						return
					}
					CheckScreen()
				}
			}
		}(worker)
	}
	wg.Wait()
}
