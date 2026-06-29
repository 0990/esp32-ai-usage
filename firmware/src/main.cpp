// ESP32 + SSD1306(128x32 I2C) 显示 Claude / Codex 额度与中转站余额。
//
// 设计：固件「只渲染」。电脑上的 host 程序通过 USB 串口发来一行 JSON（NDJSON），
// 每行就是「一整屏」(Frame)。翻页/选择显示哪页/是否过期，全部由 host 决定，
// 固件收到一行就把它画出来，自身不含任何切换逻辑。协议见 PROTOCOL.md。
//
//   pct  页：{"ic":"claude","k":"pct","l1":"5h","p1":94,"s1":"15:47","l2":"1w","p2":38,"s2":"06-25 14:30"}
//   money页：{"ic":"$","k":"money","l1":"Today","v1":"$1.2","l2":"Left","v2":"$880"}

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

// SSD1306 128x32，硬件 I2C，全缓冲。
U8G2_SSD1306_128X32_UNIVISION_F_HW_I2C u8g2(U8G2_R0, /*reset=*/U8X8_PIN_NONE);

// 24x24 单色 provider 图标（XBM，LSB-first），画在屏幕最左侧。
static const int ICON_W = 24, ICON_H = 24;
static const unsigned char ICON_CLAUDE[] U8X8_PROGMEM = {
  0x00, 0x00, 0x00,  0x00, 0x00, 0x00,  0x00, 0x00, 0x00,  0xF0, 0xFF, 0x0F,
  0xF0, 0xFF, 0x0F,  0xF0, 0x3C, 0x0F,  0xF0, 0x3C, 0x0F,  0xFE, 0x3C, 0x7F,
  0xFE, 0x3C, 0x7F,  0xFE, 0x3C, 0x7F,  0xF0, 0xFF, 0x0F,  0xF0, 0xFF, 0x0F,
  0xF0, 0xFF, 0x0F,  0x60, 0xC3, 0x06,  0x60, 0xC3, 0x06,  0x60, 0xC3, 0x06,
  0x60, 0xC3, 0x06,  0x00, 0x00, 0x00,  0x00, 0x00, 0x00,  0x00, 0x00, 0x00,
  0x00, 0x00, 0x00,  0x00, 0x00, 0x00,  0x00, 0x00, 0x00,  0x00, 0x00, 0x00
};
static const unsigned char ICON_CODEX[] U8X8_PROGMEM = {
  0x00, 0x00, 0x00,  0x00, 0x00, 0x00,  0x00, 0x00, 0x00,  0x00, 0x00, 0x00,
  0x00, 0xFF, 0x00,  0x00, 0xFF, 0x00,  0xC0, 0xFF, 0x03,  0xE0, 0xFF, 0x07,
  0xF8, 0xFC, 0x1F,  0xF8, 0xF9, 0x1F,  0xF8, 0xF3, 0x1F,  0xF8, 0xE7, 0x1F,
  0xF8, 0xE7, 0x1F,  0xF8, 0xF3, 0x1F,  0xF8, 0x19, 0x1C,  0xF8, 0x1C, 0x1C,
  0xE0, 0xFF, 0x07,  0xC0, 0xFF, 0x03,  0x00, 0xFF, 0x00,  0x00, 0xFF, 0x00,
  0x00, 0x00, 0x00,  0x00, 0x00, 0x00,  0x00, 0x00, 0x00,  0x00, 0x00, 0x00
};

// host 已不再发心跳频率信息；超过这么久没收到新行就本地兜底标 stale。
static const uint32_t STALE_MS = 90000;

// 一帧 = 一整屏。kind 决定怎么渲染。
struct Frame {
  char ic[10] = "";     // pct: "claude"/"codex"；money: 货币符号（画在硬币上）
  char k[8]   = "pct";  // "pct" | "money"
  char l1[14] = "";     // 第 1 行标签
  char l2[14] = "";     // 第 2 行标签
  int  p1     = -1;     // pct: 剩余百分比（-1=ERR）
  int  p2     = -1;
  char s1[14] = "";     // pct: 重置时间
  char s2[14] = "";
  char v1[16] = "";     // money: 金额文本
  char v2[16] = "";
  bool old    = false;  // 数据过期
  bool valid  = false;  // 是否收到过有效帧
};

Frame    frame;
uint32_t lastUpdateMs = 0;  // 最近一次成功解析的时间，0 表示从未收到
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

void parseLine(const String &line) {
  if (line.length() < 2) return;
  JsonDocument doc;
  if (deserializeJson(doc, line)) return;  // 非法 JSON 直接忽略

  Frame f;
  strlcpy(f.ic, doc["ic"] | "", sizeof(f.ic));
  strlcpy(f.k,  doc["k"]  | "pct", sizeof(f.k));
  strlcpy(f.l1, doc["l1"] | "", sizeof(f.l1));
  strlcpy(f.l2, doc["l2"] | "", sizeof(f.l2));
  f.p1 = doc["p1"] | -1;
  f.p2 = doc["p2"] | -1;
  strlcpy(f.s1, doc["s1"] | "", sizeof(f.s1));
  strlcpy(f.s2, doc["s2"] | "", sizeof(f.s2));
  strlcpy(f.v1, doc["v1"] | "", sizeof(f.v1));
  strlcpy(f.v2, doc["v2"] | "", sizeof(f.v2));
  f.old   = doc["old"] | false;
  f.valid = true;

  frame = f;
  lastUpdateMs = millis();
  dirty = true;
}

// ---- 渲染 ----
// pct 单行：右侧 16px 大字百分比，左上标签 + 进度条，左下重置时间小字。
// pct<0 → 显示 ERR。
void drawPctRow(int yTop, int xL, int xR, const char *label, int pct, const char *sub) {
  bool hasVal = pct >= 0;
  int pctX;  // 百分比区左边界（进度条右界）
  if (hasVal) {
    char num[6];
    snprintf(num, sizeof(num), "%d", pct);
    u8g2.setFont(u8g2_font_helvB14_tr);
    int nw = u8g2.getStrWidth(num);
    u8g2.setFont(u8g2_font_6x10_tf);
    int pw = u8g2.getStrWidth("%");
    pctX = xR - nw - 1 - pw;
    u8g2.setFont(u8g2_font_helvB14_tr);
    u8g2.drawStr(pctX, yTop + 13, num);
    u8g2.setFont(u8g2_font_6x10_tf);
    u8g2.drawStr(pctX + nw + 1, yTop + 13, "%");
  } else {
    u8g2.setFont(u8g2_font_helvB14_tr);
    int pw = u8g2.getStrWidth("ERR");
    pctX = xR - pw;
    u8g2.drawStr(pctX, yTop + 13, "ERR");
  }

  u8g2.setFont(u8g2_font_5x7_tr);
  u8g2.drawStr(xL, yTop + 6, label);

  int barX = xL + 13;
  int barRight = pctX - 3;
  int barW = barRight - barX;
  if (barW > 6) {
    u8g2.drawFrame(barX, yTop, barW, 7);
    if (hasVal) {
      int fill = (barW - 2) * pct / 100;
      if (fill > 0) u8g2.drawBox(barX + 1, yTop + 1, fill, 5);
    }
  }

  u8g2.setFont(u8g2_font_5x7_tr);
  u8g2.drawStr(xL, yTop + 15, (sub && sub[0]) ? sub : "--");
}

// money 单行：左侧大字标签（Used/Today/Left），右侧大字金额。
void drawMoneyRow(int yTop, int xL, int xR, const char *label, const char *val) {
  u8g2.setFont(u8g2_font_helvB12_tr);          // 标签放大（feature 2）
  u8g2.drawStr(xL, yTop + 13, (label && label[0]) ? label : "--");
  u8g2.setFont(u8g2_font_helvB14_tr);
  const char *v = (val && val[0]) ? val : "--";
  int vw = u8g2.getStrWidth(v);
  u8g2.drawStr(xR - vw, yTop + 13, v);
}

// pct 帧：左侧 provider 图标 + 两行百分比。
void drawPctFrame(const Frame &f, bool stale) {
  u8g2.clearBuffer();
  const unsigned char *icon = (strcmp(f.ic, "codex") == 0) ? ICON_CODEX : ICON_CLAUDE;
  u8g2.drawXBMP(0, (32 - ICON_H) / 2, ICON_W, ICON_H, icon);

  const int xL = ICON_W + 2;
  const int xR = 124;
  drawPctRow(0,  xL, xR, f.l1[0] ? f.l1 : "5h", f.p1, f.s1);
  drawPctRow(16, xL, xR, f.l2[0] ? f.l2 : "1w", f.p2, f.s2);

  if (stale) {
    u8g2.setFont(u8g2_font_5x7_tr);
    u8g2.drawStr(xL + 34, 15, "old");
  }
  u8g2.sendBuffer();
}

// money 帧：左侧画一枚带货币符号的硬币，右侧两行金额。
void drawMoneyFrame(const Frame &f, bool stale) {
  u8g2.clearBuffer();

  u8g2.drawDisc(11, 16, 9);
  u8g2.setDrawColor(0);
  u8g2.setFont(u8g2_font_helvB12_tr);
  const char *one = "T";                     // money 帧来自中转站，硬币上画大写 T
  int dw = u8g2.getStrWidth(one);
  u8g2.drawStr(11 - dw / 2, 23, one);
  u8g2.setDrawColor(1);

  const int xL = ICON_W + 2;
  const int xR = 124;
  drawMoneyRow(0,  xL, xR, f.l1[0] ? f.l1 : "Used", f.v1);
  drawMoneyRow(16, xL, xR, f.l2[0] ? f.l2 : "Left", f.v2);

  if (stale) {
    u8g2.setFont(u8g2_font_5x7_tr);
    u8g2.drawStr(xR - u8g2.getStrWidth("old"), 7, "old");
  }
  u8g2.sendBuffer();
}

void drawWaiting() {
  u8g2.clearBuffer();
  u8g2.setFont(u8g2_font_7x13B_tr);
  const char *t = "AI Usage";
  u8g2.drawStr((128 - u8g2.getStrWidth(t)) / 2, 13, t);
  u8g2.setFont(u8g2_font_5x7_tr);
  const char *s = "Waiting for PC...";
  u8g2.drawStr((128 - u8g2.getStrWidth(s)) / 2, 28, s);
  u8g2.sendBuffer();
}

void setup() {
  Serial.begin(115200);
  Wire.begin(PIN_SDA, PIN_SCL);
  u8g2.setI2CAddress(0x3C * 2);
  u8g2.begin();
  drawWaiting();
}

void loop() {
  pumpSerial();

  uint32_t now = millis();
  static uint32_t lastDraw = 0;
  // host 会自带 old 标记；这里再加一道本地兜底（host 长时间不发就标过期）。
  bool stale = frame.old || (lastUpdateMs != 0 && now - lastUpdateMs > STALE_MS);

  // 数据有变化时重画；另外每秒强制刷新一次以更新 stale 标记。
  if (dirty || now - lastDraw > 1000) {
    if (lastUpdateMs == 0 || !frame.valid) {
      drawWaiting();
    } else if (strcmp(frame.k, "money") == 0) {
      drawMoneyFrame(frame, stale);
    } else {
      drawPctFrame(frame, stale);
    }
    lastDraw = now;
    dirty = false;
  }

  delay(10);
}
