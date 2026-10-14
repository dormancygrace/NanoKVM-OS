#!/usr/bin/env python3
"""Exercise actual RTL8733BS RF argument parsing on the host, never a radio.

Pass --driver pointing to the pinned driver with platform patches applied.
The command tokenizer and RF handlers are extracted from that tree; command
routing, debug output and the RF state container are host doubles.
"""
import argparse
from pathlib import Path
import subprocess

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--driver', type=Path, required=True)
p.add_argument('--output', type=Path, required=True)
a = p.parse_args()
a.output.mkdir(parents=True, exist_ok=True)


def function(path, signature):
    text = (a.driver / path).read_text()
    start = text.index(signature)
    opening = text.index('{', start)
    level, end = 1, opening + 1
    while level:
        level += (text[end] == '{') - (text[end] == '}')
        end += 1
    return text[start:end]


source = r"""
#define _GNU_SOURCE
#include <assert.h>
#include <stdint.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
typedef uint8_t u8;
typedef uint32_t u32;
typedef int32_t s32;
#define MAX_ARGC 20
#define MAX_ARGV 16
#define DCMD_DECIMAL "%d"
#define PHYDM_SSCANF(x,y,z) sscanf(x,y,(int *)(z))
#define BIT(x) (1u << (x))
#define PDM_SNPF(...) do {} while (0)
struct _hal_rf_ { u32 rf_dbg_comp, rf_supportability; };
struct dm_struct { struct _hal_rf_ rf_table; };
"""
source += function('hal/phydm/halrf/halrf_debug.c', 'void halrf_debug_trace(')
source += '\n' + function('hal/phydm/halrf/halrf.c', 'void halrf_support_ability_debug(')
source += r"""
static void phydm_cmd_parser(struct dm_struct *dm, char input[][MAX_ARGV],
                            u32 input_num, u8 flag, char *output, u32 out_len) {
    u32 used = 0;
    assert(input_num >= 7 && flag == 1 && !strcmp(input[0], "rf"));
    if (!strcmp(input[1], "dbg"))
        halrf_debug_trace(dm, input, &used, output, &out_len);
    else {
        assert(!strcmp(input[1], "ability"));
        halrf_support_ability_debug(dm, input, &used, output, &out_len);
    }
}
"""
source += '\n' + function('hal/phydm/phydm_debug.c', 's32 phydm_cmd(')
source += r"""
static unsigned cases;
static void run(const char *command, u32 initial, u32 expected) {
    struct dm_struct dm = {.rf_table = {initial, initial}};
    char input[256], output[1] = {0};
    /* Supply every row these handlers scan: no uninitialized trailing argv. */
    snprintf(input, sizeof(input), "%s 0 0 0 0 0 0 0", command);
    assert(phydm_cmd(&dm, input, strlen(input), 1, output, sizeof(output)) == 0);
    u32 actual = strstr(command, "ability") ? dm.rf_table.rf_supportability
                                          : dm.rf_table.rf_dbg_comp;
    if (actual != expected) {
        fprintf(stderr, "%s: expected %u got %u\n", command, expected, actual);
        exit(1);
    }
    ++cases;
}
int main(void) {
    run("rf dbg 0 1", 0, 1); run("rf ability 0 1", 0, 1);
    run("rf dbg  1", 0, 1); run("rf ability  1", 0, 1);
    run("rf,dbg,,1", 0, 1); run("rf,ability,,1", 0, 1);
    run("rf dbg  2", 1, 0); run("rf ability  2", 1, 0);
    run("rf dbg 3 1", 0, 8); run("rf ability 3 2", 8, 0);
    run("rf dbg 101", 7, 0); run("rf ability 100", 7, 7);
    printf("PASS %u RF parser cases: spaces/commas, empty arguments, enable/disable, display/reset\n", cases);
    return 0;
}
"""
target = a.output / 'rf-parser-contract.c'
target.write_text(source)
subprocess.run(['cc', '-std=gnu11', '-Wall', '-Wextra', '-Werror',
                '-Wno-unused-parameter', '-fsanitize=address,undefined',
                '-fno-omit-frame-pointer', '-g', str(target),
                '-o', str(a.output / 'rf-parser-contract')], check=True)
subprocess.run([str(a.output / 'rf-parser-contract')], check=True)
