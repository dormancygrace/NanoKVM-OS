#pragma once

// The server runs ahead of the other services while it starts and its first viewer connects, then keeps a little
// ahead of ordinary tasks, as the Go server's internal/startpriority: every thread at nice -10 from the start until
// 5 s after the first viewer (a minute at most), then nice -5 (at 0 another CPU-bound task gets the single core in
// equal turns and the video threads run late). Linux sets nice per thread and new threads inherit it, so raising
// at the start covers the threads created later; releasing visits every thread again. Also the OOM protection
// (oom_score_adj -300, as kvm_system).

namespace nkos::system::startpriority {

// Before the other threads start.
void raise();
// From the loop thread: the first viewer connected.
void viewer_connected();

} // namespace nkos::system::startpriority
