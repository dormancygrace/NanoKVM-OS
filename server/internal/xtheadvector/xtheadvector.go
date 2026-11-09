// Package xtheadvector states whether this build runs XTheadVector (RVV
// 0.7.1) code: the T-Head C906 vector unit of the SG2002.
//
// NanoKVM OS runs only on the SG2002, and its kernel enables vector state for
// every user thread, so Available is a build-time constant: true for
// linux/riscv64, false on the host and with the nanokvmqemu build tag (QEMU
// has no XTheadVector).
package xtheadvector
