// ESP32 + SSD1306(128x64 I2C) 显示 Claude / Codex 的 5小时、1周剩余额度。
//
// 数据来源：电脑上的 host 程序通过 USB 串口发来一行 JSON（NDJSON）：
//   {"claude":{"h5":{"left":94,"reset":"15:47"},"wk":{"left":38,"reset":"06-25"},"ok":true},
//    "codex":{"h5":{"left":91,"reset":"20:48"},"wk":{"left":35,"reset":"06-25"},"ok":true}}
//
// 显示：每约 4 秒在 CLAUDE 页 / CODEX 页之间自动翻页，每页两行（5h / 1w）大字 + 进度条 + 重置时间。

#include <Arduino.h>
#include <U8g2lib.h>
#include <Wire.h>
#include <ArduinoJson.h>

#ifndef PIN_SDA
#define PIN_SDA 8
#endif
#ifndef PIN_SCL
#define PIN_SCL 9
#endif

// SSD1306 128x64，硬件 I2C，全缓冲。
U8G2_SSD1306_128X64_NONAME_F_HW_I2C u8g2(U8G2_R0, /*reset=*/U8X8_PIN_NONE);

static const uint32_t PAGE_MS   = 4000;   // 翻页间隔
static const uint32_t STALE_MS  = 90000;  // 超过这么久没新数据就标 stale

struct Win {
  int  left = -1;        // 剩余百分比，-1 表示无数据
  char reset[12] = "--";
};
struct Prov {
  Win  h5;
  Win  wk;
  bool ok = false;
};

Prov claude, codex;
uint32_t lastUpdateMs = 0;   // 最近一次成功解析的时间，0 表示从未收到
uint32_t lastPageMs   = 0;
uint8_t  page         = 0;   // 0=CLAUDE, 1=CODEX
bool     dirty        = true;

String rxBuf;

// ---- 串口收行 ----
void parseLine(const String &line);

void pumpSerial() {
  while (Serial.available()) {
    char c = (char)Serial.read();
    if (c == '\n') {
      parseLine(rxBuf);
      rxBuf = "";
    } else if (c != '\r') {
      rxBuf += c;
      if (rxBuf.length() > 600) rxBuf = "";  // 防御超长非法行
    }
  }
}

void fillWin(JsonObjectConst o, Win &w) {
  w.left = o["left"] | -1;
  const char *r = o["reset"] | "--";
  strlcpy(w.reset, r, sizeof(w.reset));
}

void fillProv(JsonObjectConst o, Prov &p) {
  if (o.isNull()) return;
  p.ok = o["ok"] | false;
  fillWin(o["h5"], p.h5);
  fillWin(o["wk"], p.wk);
}

void parseLine(const String &line) {
  if (line.length() < 2) return;
  JsonDocument doc;
  if (deserializeJson(doc, line)) return;  // 非法 JSON 直接忽略
  fillProv(doc["claude"].as<JsonObjectConst>(), claude);
  fillProv(doc["codex"].as<JsonObjectConst>(), codex);
  lastUpdateMs = millis();
  dirty = true;
}

// ---- 渲染 ----
// 画一行：yTop 起始，label 如 "5h"，根据 ok/left 画进度条+百分比，下面小字重置时间。
void drawRow(int yTop, const char *label, const Win &w, bool ok) {
  char pct[8];
  bool hasVal = ok && w.left >= 0;
  if (hasVal) snprintf(pct, sizeof(pct), "%d%%", w.left);
  else        snprintf(pct, sizeof(pct), "ERR");

  // 左侧标签
  u8g2.setFont(u8g2_font_6x12_tr);
  u8g2.drawStr(0, yTop + 8, label);

  // 右侧百分比（大字）
  u8g2.setFont(u8g2_font_7x13B_tr);
  int pw = u8g2.getStrWidth(pct);
  u8g2.drawStr(128 - pw, yTop + 9, pct);

  // 中间进度条
  int barX = 18;
  int barRight = 128 - pw - 4;
  int barW = barRight - barX;
  if (barW > 6) {
    u8g2.drawFrame(barX, yTop, barW, 9);
    if (hasVal) {
      int fill = (barW - 2) * w.left / 100;
      if (fill > 0) u8g2.drawBox(barX + 1, yTop + 1, fill, 7);
    }
  }

  // 重置时间（小字）
  u8g2.setFont(u8g2_font_5x7_tr);
  char rs[20];
  snprintf(rs, sizeof(rs), "reset %s", w.reset);
  u8g2.drawStr(18, yTop + 16, rs);
}

void drawProvider(const char *name, const Prov &p, bool stale) {
  u8g2.clearBuffer();

  // 标题居中
  u8g2.setFont(u8g2_font_7x13B_tr);
  int tw = u8g2.getStrWidth(name);
  u8g2.drawStr((128 - tw) / 2, 11, name);
  if (stale) {  // 右上角提示数据过期
    u8g2.setFont(u8g2_font_5x7_tr);
    u8g2.drawStr(108, 7, "old");
  }
  u8g2.drawHLine(0, 14, 128);

  drawRow(17, "5h", p.h5, p.ok);
  drawRow(39, "1w", p.wk, p.ok);

  // 底部页码点
  u8g2.drawDisc(58, 63, 1);
  u8g2.drawDisc(70, 63, 1);
  if (page == 0) u8g2.drawDisc(58, 63, 2); else u8g2.drawDisc(70, 63, 2);

  u8g2.sendBuffer();
}

void drawWaiting() {
  u8g2.clearBuffer();
  u8g2.setFont(u8g2_font_7x13B_tr);
  const char *t = "AI Credits";
  u8g2.drawStr((128 - u8g2.getStrWidth(t)) / 2, 26, t);
  u8g2.setFont(u8g2_font_6x12_tr);
  const char *s = "Waiting for PC...";
  u8g2.drawStr((128 - u8g2.getStrWidth(s)) / 2, 44, s);
  u8g2.sendBuffer();
}

void setup() {
  Serial.begin(115200);
  Wire.begin(PIN_SDA, PIN_SCL);
  u8g2.setI2CAddress(0x3C * 2);
  u8g2.begin();
  drawWaiting();
  lastPageMs = millis();
}

void loop() {
  pumpSerial();

  uint32_t now = millis();
  if (now - lastPageMs >= PAGE_MS) {
    page ^= 1;
    lastPageMs = now;
    dirty = true;
  }

  static uint32_t lastDraw = 0;
  bool stale = (lastUpdateMs != 0) && (now - lastUpdateMs > STALE_MS);
  // 数据/翻页有变化时重画；另外每秒强制刷新一次以更新 stale 标记。
  if (dirty || now - lastDraw > 1000) {
    if (lastUpdateMs == 0) {
      drawWaiting();
    } else if (page == 0) {
      drawProvider("CLAUDE", claude, stale);
    } else {
      drawProvider("CODEX", codex, stale);
    }
    lastDraw = now;
    dirty = false;
  }

  delay(10);
}
