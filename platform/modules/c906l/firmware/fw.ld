/* SPDX-License-Identifier: GPL-2.0-only */
/* The small core's program: a static PIE at 0, relocated by start.S. STACK: from the build. */
OUTPUT_ARCH(riscv)
ENTRY(_start)
SECTIONS
{
	. = 0;
	.text : { *(.text.start) *(.text*) }
	.rodata : { *(.rodata*) *(.srodata*) }
	.data.rel.ro : { *(.data.rel.ro*) }
	.got : { *(.got*) }
	.data : { *(.data*) *(.sdata*) }
	.rela.dyn : { __rela_start = .; *(.rela*) __rela_end = .; }
	.dynamic : { *(.dynamic) }
	.dynsym : { *(.dynsym) }
	.dynstr : { *(.dynstr) }
	.hash : { *(.hash) *(.gnu.hash) }
	. = ALIGN(64);
	__bss_start = .;
	.bss (NOLOAD) : { *(.bss*) *(.sbss*) *(COMMON) }
	. = ALIGN(16);
	__bss_end = .;
	. += STACK;
	__stack_top = .;
	ASSERT(__stack_top <= PROGRAM_MAX, "program too large")
	/DISCARD/ : { *(.comment) *(.note*) *(.riscv.attributes) *(.eh_frame*) *(.interp) }
}
