# 向 ESP32 串口发送一行测试 Frame，驱动 OLED 显示（无需 host 程序）。
# 新协议：一行 = 一整屏（Frame）。详见 PROTOCOL.md。
# 用法示例：
#   .\send-test.ps1                              # 默认 COM3，pct 页
#   .\send-test.ps1 -Kind pct -Icon codex        # Codex 百分比页
#   .\send-test.ps1 -Kind money -V1 "$1.2" -V2 "$880"   # 中转站金额页
#
# 注意：host 程序占用串口时不能同时运行本脚本（串口独占）。

param(
  [string]$Port = "COM3",
  [ValidateSet("pct","money")]
  [string]$Kind = "pct",
  [string]$Icon = "claude",       # pct: claude/codex；money: 货币符号
  # pct 页参数
  [int]$P1 = 94, [string]$S1 = "15:47",
  [int]$P2 = 38, [string]$S2 = "06-25 14:30",
  # money 页参数
  [string]$V1 = "$1.2", [string]$V2 = "$880"
)

if ($Kind -eq "money") {
  $frame = @{ ic = $Icon; k = "money"; l1 = "Today"; v1 = $V1; l2 = "Left"; v2 = $V2 }
} else {
  $frame = @{ ic = $Icon; k = "pct"; l1 = "5h"; p1 = $P1; s1 = $S1; l2 = "1w"; p2 = $P2; s2 = $S2 }
}
$line = ($frame | ConvertTo-Json -Compress -Depth 5)

$sp = New-Object System.IO.Ports.SerialPort $Port,115200,None,8,one
$sp.DtrEnable = $false   # 避免开口时复位 ESP32
$sp.RtsEnable = $false
$sp.Open()
Start-Sleep -Milliseconds 400
1..3 | ForEach-Object { $sp.WriteLine($line); Start-Sleep -Milliseconds 150 }
Start-Sleep -Milliseconds 200
$sp.Close()
Write-Host "sent to $Port (kind=$Kind): $line"
