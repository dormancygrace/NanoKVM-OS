/* SPDX-License-Identifier: GPL-2.0-only WITH Linux-syscall-note */
/*
 * /dev/nanokvm-opus: an Opus encoder on the SG2002's small core (C906L) for
 * user space. One encoder per open file (two at a time), 48 kHz stereo,
 * OPUS_APPLICATION_AUDIO, 20 ms frames of 16-bit interleaved PCM.
 */
#ifndef NANOKVM_OPUS_H
#define NANOKVM_OPUS_H

#include <linux/ioctl.h>
#include <linux/types.h>

#define NANOKVM_OPUS_FRAME_BYTES (960 * 2 * 2)

/* Before the first frame: Opus' OPUS_SET_BITRATE, _COMPLEXITY, _VBR_CONSTRAINT. */
struct nanokvm_opus_setup {
	__u32 bitrate;
	__u32 complexity;
	__u32 vbr_constraint;
	__u32 reserved;
};
#define NANOKVM_OPUS_SETUP _IOW('K', 0xd0, struct nanokvm_opus_setup)

/* One frame: pcm (NANOKVM_OPUS_FRAME_BYTES) -> packet; returns the packet's bytes. */
struct nanokvm_opus_encode {
	__u64 pcm;
	__u64 packet;
	__u32 packet_size;
	__u32 reserved;
};
#define NANOKVM_OPUS_ENCODE _IOW('K', 0xd1, struct nanokvm_opus_encode)

#endif
