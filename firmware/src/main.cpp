// ESP32 + SSD1306(128x32 I2C) 显示 Claude / Codex / Kimi 额度与中转站余额。
//
// 设计：固件「只渲染」。电脑上的 host 程序通过 USB 串口发来一行 JSON（NDJSON），
// 每行就是「一整屏」(Frame)。翻页/选择显示哪页/是否过期，全部由 host 决定，
// 固件收到一行就把它画出来，自身不含任何切换逻辑。协议见 PROTOCOL.md。
//
//   pct  页：{"ic":"claude","k":"pct","l1":"5h","p1":94,"s1":"15:47","l2":"1w","p2":38,"s2":"06-25 14:30"}
//          （ic 还可为 "codex" / "kimi"）
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
#ifndef PIN_RGB
#define PIN_RGB 8      // ESP32-C3 SuperMini 板载 WS2812 RGB 状态灯
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
// Kimi 图标：字母 "K"。
static const unsigned char ICON_KIMI[] U8X8_PROGMEM = {
  0x00, 0x00, 0x00,  0x00, 0x00, 0x00,  0x00, 0x00, 0x00,  0x70, 0x00, 0x0E,
  0x70, 0x00, 0x07,  0x70, 0x80, 0x03,  0x70, 0xC0, 0x01,  0x70, 0x70, 0x00,
  0x70, 0x38, 0x00,  0x70, 0x1C, 0x00,  0x70, 0x0E, 0x00,  0x70, 0x0E, 0x00,
  0x70, 0x1C, 0x00,  0x70, 0x38, 0x00,  0x70, 0x70, 0x00,  0x70, 0xE0, 0x00,
  0x70, 0xC0, 0x01,  0x70, 0x80, 0x03,  0x70, 0x00, 0x07,  0x70, 0x00, 0x0E,
  0x70, 0x00, 0x1C,  0x00, 0x00, 0x00,  0x00, 0x00, 0x00,  0x00, 0x00, 0x00
};

// host 已不再发心跳频率信息；超过这么久没收到新行就本地兜底标 stale。
static const uint32_t STALE_MS = 90000;

// Frame.a 的取值（与 host 的 Act* 一致）。
static const int ActIdle_ = 0, ActBusy_ = 1, ActAttention_ = 2;

// 一帧 = 一整屏。kind 决定怎么渲染。
struct Frame {
  char ic[10] = "";     // pct: "claude"/"codex"/"kimi"；money: 货币符号（画在硬币上）
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
  int  a      = 0;      // 工作状态：0=空闲 1=工作中（跑动的点） 2=需要你（图标闪烁）
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
  f.a     = doc["a"] | 0;
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

// 「需要你」角标：图标区右上角一枚 9px 圆形角标，中间一个「!」，外圈 1px 黑边把它和图标隔开
// （像手机 App 的红点角标）。每 400ms 在「实心圆 + 镂空 !」和「空心圆环 + 实心 !」之间切换，
// 有脉动但不再整块反色。中心 (19,4) 只压住图标右上角约 3 行像素；money 页的硬币顶在 y=7，不相交。
void drawAttentionBadge() {
  const int cx = 19, cy = 4, r = 4;
  bool filled = (millis() / 400) % 2 == 0;
  u8g2.setDrawColor(0);
  u8g2.drawDisc(cx, cy, r + 1);            // 黑边
  u8g2.setDrawColor(1);
  if (filled) {
    u8g2.drawDisc(cx, cy, r);
    u8g2.setDrawColor(0);                  // 镂空的「!」
  } else {
    u8g2.drawCircle(cx, cy, r);
  }
  u8g2.drawVLine(cx, cy - 2, 3);           // 竖杠 3px + 空 1px + 点，正好居中在圆内
  u8g2.drawPixel(cx, cy + 2);
  u8g2.setDrawColor(1);
}

// 工作状态标志，叠加在左侧图标区（x 0..23）：
//   a=1 工作中：底部一排 3 个点轮流点亮（各 provider 图标底部 y>=25 都是空的，不会压到图标）；
//   a=2 需要你：右上角脉动的「!」角标（drawAttentionBadge）。
// 在 clearBuffer 之后、sendBuffer 之前调用。
void drawActivity(int a) {
  if (a == ActBusy_) {
    int phase = (millis() / 300) % 3;
    for (int i = 0; i < 3; i++) {
      int x = 5 + i * 6;
      if (i == phase) u8g2.drawDisc(x, 29, 2);
      else            u8g2.drawPixel(x, 29);
    }
  } else if (a == ActAttention_) {
    drawAttentionBadge();
  }
}

// pct 帧：左侧 provider 图标 + 两行百分比。
void drawPctFrame(const Frame &f, bool stale) {
  u8g2.clearBuffer();
  const unsigned char *icon = ICON_CLAUDE;
  if (strcmp(f.ic, "codex") == 0) icon = ICON_CODEX;
  else if (strcmp(f.ic, "kimi") == 0) icon = ICON_KIMI;
  u8g2.drawXBMP(0, (32 - ICON_H) / 2, ICON_W, ICON_H, icon);

  const int xL = ICON_W + 2;
  const int xR = 124;
  drawPctRow(0,  xL, xR, f.l1[0] ? f.l1 : "5h", f.p1, f.s1);
  drawPctRow(16, xL, xR, f.l2[0] ? f.l2 : "1w", f.p2, f.s2);

  if (stale) {
    u8g2.setFont(u8g2_font_5x7_tr);
    u8g2.drawStr(xL + 34, 15, "old");
  }
  drawActivity(f.a);
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
  drawActivity(f.a);
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

// ---- 板载 RGB 状态灯（PIN_RGB 上的 WS2812，用核心自带 neopixelWrite 驱动）----
// 蓝=数据正常，红=数据异常(ERR)，黄=数据过期(old)，红闪=等待电脑，
// 蓝呼吸=CLI 工作中，紫闪=CLI 在等你授权。
// 亮度压低避免刺眼；底层按「实际写入色」去重，减少无谓的 bit-bang。
enum LedState { LED_WAIT, LED_OK, LED_ERR, LED_OLD, LED_BUSY, LED_ATTN };

// 只有目标色和上次写入的不同才真正驱动灯珠。
void writeLed(uint8_t r, uint8_t g, uint8_t b) {
  static long last = -1;
  long cur = ((long)r << 16) | ((long)g << 8) | b;
  if (cur == last) return;
  last = cur;
  neopixelWrite(PIN_RGB, r, g, b);
}

void setLed(LedState s) {
  switch (s) {
    case LED_OK:   writeLed(0,  14, 30); break;  // 蓝（掺点绿去掉紫味）
    case LED_ERR:  writeLed(48, 0,  0);  break;  // 红
    case LED_OLD:  writeLed(48, 28, 0);  break;  // 黄
    case LED_WAIT:                               // 等待电脑：红灯闪烁（~2Hz）
      if ((millis() / 500) % 2 == 0) writeLed(48, 0, 0);
      else                           writeLed(0,  0, 0);
      break;
    case LED_BUSY: {                             // 工作中：蓝色呼吸（2s 一个来回）
      uint32_t t = millis() % 2000;
      uint32_t k = t < 1000 ? t : 2000 - t;      // 0..1000..0
      k = 100 + k * 900 / 1000;                  // 保底亮度，别完全熄灭
      writeLed(0, 14 * k / 1000, 30 * k / 1000);
      break;
    }
    case LED_ATTN:                               // 需要你：紫色闪烁（~2Hz）
      if ((millis() / 250) % 2 == 0) writeLed(40, 0, 40);
      else                           writeLed(0,  0, 0);
      break;
  }
}

// 帧是否处于异常态：pct 任一窗口为 ERR(-1)，或 money 金额两行全空。
bool frameHasError(const Frame &f) {
  if (strcmp(f.k, "money") == 0) return f.v1[0] == '\0' && f.v2[0] == '\0';
  return f.p1 < 0 || f.p2 < 0;
}

void setup() {
  Serial.begin(115200);
  Wire.begin(PIN_SDA, PIN_SCL);
  u8g2.setI2CAddress(0x3C * 2);
  u8g2.begin();
  setLed(LED_WAIT);
  drawWaiting();
}

void loop() {
  pumpSerial();

  uint32_t now = millis();
  static uint32_t lastDraw = 0;
  // host 会自带 old 标记；这里再加一道本地兜底（host 长时间不发就标过期）。
  bool stale = frame.old || (lastUpdateMs != 0 && now - lastUpdateMs > STALE_MS);

  // 状态灯：等待→红闪，需要你→紫闪，过期→黄，异常→红，工作中→蓝呼吸，正常→蓝。
  if (lastUpdateMs == 0 || !frame.valid) {
    setLed(LED_WAIT);
  } else if (frame.a == ActAttention_) {
    setLed(LED_ATTN);
  } else if (stale) {
    setLed(LED_OLD);
  } else if (frameHasError(frame)) {
    setLed(LED_ERR);
  } else if (frame.a == ActBusy_) {
    setLed(LED_BUSY);
  } else {
    setLed(LED_OK);
  }

  // 数据有变化时重画；另外每秒强制刷新一次以更新 stale 标记；有工作状态动画时 150ms 一帧。
  uint32_t redrawMs = (frame.valid && frame.a != ActIdle_) ? 150 : 1000;
  if (dirty || now - lastDraw > redrawMs) {
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
