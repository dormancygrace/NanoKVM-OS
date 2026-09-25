#include <errno.h>
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include "sample_comm.h"
#include "ini.h"
#include "nanokvm_capture_size.h"

extern ISP_SNS_STATE_S *g_pastLt6911[VI_MAX_PIPE_NUM];
extern ISP_SNS_OBJ_S stSnsLT6911_Obj;
#define CHECK(test) do { if (!(test)) { fprintf(stderr, "FAIL %d: %s\n", __LINE__, #test); return 1; } } while (0)
static int put(const char *dir, const char *name, const char *text)
{
    char path[256];
    snprintf(path, sizeof(path), "%s/%s", dir, name);
    FILE *f = fopen(path, "w");
    if (!f) return -1;
    int failed = fputs(text, f) == EOF;
    return fclose(f) || failed ? -1 : 0;
}
static int ini_handler_test(void *user, const char *section, const char *name, const char *value)
{
    if (!strcmp(section, "sensor") && !strcmp(name, "bus_id") && !strcmp(value, "4")) (*(int *)user)++;
    return 1;
}
int main(void)
{
    char dir[] = "/tmp/nanokvm-config-XXXXXX", path[256];
    CHECK(mkdtemp(dir) != NULL);
    uint32_t w = 1, h = 2;
    CHECK(nanokvm_read_capture_size(dir, &w, &h) == 0 && w == 1920 && h == 1080);
    CHECK(put(dir, "width", "1280\n") == 0);
    w = 7; h = 9;
    CHECK(nanokvm_read_capture_size(dir, &w, &h) != 0 && w == 7 && h == 9);
    CHECK(put(dir, "height", "720\n") == 0);
    CHECK(nanokvm_read_capture_size(dir, &w, &h) == 0 && w == 1280 && h == 720);
    const char *invalid[] = {"", "0", "-1", "12x", "65536", "999999999999999999999999999999999999999999", "640\n480\n"};
    for (size_t i = 0; i < sizeof(invalid) / sizeof(invalid[0]); i++) {
        CHECK(put(dir, "width", invalid[i]) == 0);
        w = 7; h = 9;
        CHECK(nanokvm_read_capture_size(dir, &w, &h) != 0 && w == 7 && h == 9);
    }
    snprintf(path, sizeof(path), "%s/width", dir); CHECK(unlink(path) == 0);
    snprintf(path, sizeof(path), "%s/height", dir); CHECK(unlink(path) == 0);
    CHECK(rmdir(dir) == 0);
    int matches = 0;
    CHECK(ini_parse_string("[sensor]\r\nbus_id = 4 ; comment\r\n", ini_handler_test, &matches) == 0 && matches == 1);

    /* Exercise actual sensor callbacks with a local context, no ISP/I2C init. */
    ISP_SNS_STATE_S state = {0};
    ISP_SENSOR_EXP_FUNC_S callbacks = {0};
    g_pastLt6911[0] = &state;
    CHECK(stSnsLT6911_Obj.pfnExpSensorCb(&callbacks) == CVI_SUCCESS);
    const unsigned sizes[][2] = {{640,480}, {1280,720}, {1920,1080}, {2560,1440}};
    for (size_t i = 0; i < sizeof(sizes) / sizeof(sizes[0]); i++) {
        ISP_CMOS_SENSOR_IMAGE_MODE_S mode = {.u16Width=sizes[i][0], .u16Height=sizes[i][1], .f32Fps=60};
        SNS_COMBO_DEV_ATTR_S rx = {0};
        CHECK(callbacks.pfn_cmos_set_image_mode(0, &mode) == CVI_SUCCESS);
        CHECK(stSnsLT6911_Obj.pfnGetRxAttr(0, &rx) == CVI_SUCCESS);
        CHECK(rx.img_size.width == sizes[i][0] && rx.img_size.height == sizes[i][1]);
        CHECK(rx.devno == 0 && rx.mipi_attr.lane_id[1] == 4 && rx.mipi_attr.pn_swap[0] == 0);
    }
    g_pastLt6911[0] = NULL;
    SAMPLE_INI_CFG_S cfg = {0};
    CHECK(SAMPLE_COMM_VI_ParseIni(&cfg) == CVI_SUCCESS);
    CHECK(cfg.enSnsType[0] == LONTIUM_LT6911_2M_60FPS_8BIT);
    PIC_SIZE_E pic;
    CHECK(SAMPLE_COMM_VI_GetSizeBySensor(cfg.enSnsType[0], &pic) == CVI_SUCCESS && pic == PIC_CUSTOMIZE);
    SIZE_S size;
    CHECK(SAMPLE_COMM_SYS_GetPicSize(PIC_CUSTOMIZE, &size) == CVI_SUCCESS);
    printf("PASS: current inih, bounded size parser, actual LT6911 callbacks at four sizes, NanoKVM lane defaults; runtime geometry %ux%u\n", size.u32Width, size.u32Height);
    return 0;
}
