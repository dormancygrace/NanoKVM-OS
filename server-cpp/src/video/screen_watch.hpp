#pragma once

// The screen watch, as the Go server's service/vm/screen_watch.go: it looks at the host's screen for a crash (a
// Windows stop screen or a kernel panic). A thumbnail every 10 s; once the screen has changed and then held still,
// the TPU classifier (nanokvm-screen with the model of nanokvm-model-screen) says what it shows. A stop screen or a
// text console is then read by the OCR when nobody watches the video (its models take the memory a viewer's video
// needs). Crash text produces a notification with the lines read and a screenshot; a sure stop screen can be reported
// without OCR while an unattended video stream holds that memory. Loop thread only.

namespace nkos::video::screen_watch {

// The classifier's model is installed and the OCR is installed and on (ScreenWatchAvailable).
bool available();

// Starts watching (StartScreenWatch), after the capture has started. While the watch is on and a channel takes its
// events, the HDMI idle timeout leaves the capture running: a crash matters most when nobody is watching.
void start();

} // namespace nkos::video::screen_watch
