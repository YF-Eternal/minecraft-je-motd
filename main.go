// 作者: YF_Eternal
// 项目: minecraft-je-motd
// 版本: 1.0.3-HTTPS-DNS
// 许可: MIT
// 描述: 一个命令行工具，用于获取并展示 Minecraft Java 版服务器的 MOTD 信息。
// 仓库: https://github.com/YF-Eternal/minecraft-je-motd/

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// DNSResponse 添加用于解析阿里 DNS JSON 响应的结构体
type DNSResponse struct {
	Status int `json:"Status"`
	Answer []struct {
		Type int    `json:"type"`
		Data string `json:"data"`
	} `json:"Answer"`
}

// ChatComponent 表示聊天组件结构体 (用于 JSON 解析)
type ChatComponent struct {
	Text  string               `json:"text,omitempty"`  // 文本内容
	Color string               `json:"color,omitempty"` // 文本颜色
	Extra []ChatComponentMixed `json:"extra,omitempty"` // 嵌套组件
}

// 聊天组件的多种可能格式（组件或纯字符串）
type ChatComponentMixed struct {
	TextComponent *ChatComponent // 若为组件
	RawString     string         // 若为纯字符串
}

// 实现自定义反序列化逻辑以处理不同格式的聊天组件
func (c *ChatComponentMixed) UnmarshalJSON(data []byte) error {
	if data[0] == '"' {
		return json.Unmarshal(data, &c.RawString)
	}
	var comp ChatComponent
	if err := json.Unmarshal(data, &comp); err != nil {
		return err
	}
	c.TextComponent = &comp
	return nil
}

func resolveMinecraftSRV(name string) (host string, port uint16, err error) {
	// 首先尝试通过阿里 DNS 进行解析
	url := fmt.Sprintf("https://223.5.5.5/resolve?name=_minecraft._tcp.%s&type=srv", name)
	client := &http.Client{Timeout: 5 * time.Second}

	fmt.Printf("正在尝试通过阿里 DNS 解析 SRV 记录...\n")
	resp, err := client.Get(url)
	if err == nil {
		defer resp.Body.Close()
		var dnsResp DNSResponse
		if err := json.NewDecoder(resp.Body).Decode(&dnsResp); err == nil {
			// 检查是否有 Answer
			if len(dnsResp.Answer) > 0 {
				for _, ans := range dnsResp.Answer {
					if ans.Type == 33 { // SRV 记录的类型是 33
						// SRV 记录格式: priority weight port target
						parts := strings.Fields(ans.Data)
						if len(parts) == 4 {
							if p, err := strconv.ParseUint(parts[2], 10, 16); err == nil {
								resolvedHost := strings.TrimSuffix(parts[3], ".")
								fmt.Printf("通过阿里 DNS 解析到 SRV 记录: %s:%d\n", resolvedHost, p)
								return resolvedHost, uint16(p), nil
							}
						}
					}
				}
			} else {
				fmt.Println("阿里 DNS 未返回 SRV 记录，尝试标准 DNS 解析...")
			}
		}
	} else {
		fmt.Printf("阿里 DNS 解析失败: %v，尝试标准 DNS 解析...\n", err)
	}

	// 如果阿里 DNS 解析失败或没有结果，使用原有的标准 DNS 解析
	fmt.Println("尝试标准 DNS 解析 SRV 记录...")
	_, addrs, err := net.LookupSRV("minecraft", "tcp", name)
	if err != nil || len(addrs) == 0 {
		fmt.Printf("未找到 SRV 记录，使用默认配置: %s:25565\n", name)
		return name, 25565, nil // 无 SRV 记录时使用默认端口
	}
	resolvedHost := strings.TrimSuffix(addrs[0].Target, ".")
	fmt.Printf("通过标准 DNS 解析到 SRV 记录: %s:%d\n", resolvedHost, addrs[0].Port)
	return resolvedHost, addrs[0].Port, nil
}

// Minecraft 颜色代码映射到 ANSI 终端颜色代码
var minecraftColorMap = map[string]string{
	"black":        "\033[30m",
	"dark_blue":    "\033[34m",
	"dark_green":   "\033[32m",
	"dark_aqua":    "\033[36m",
	"dark_red":     "\033[31m",
	"dark_purple":  "\033[35m",
	"gold":         "\033[33m",
	"gray":         "\033[37m",
	"dark_gray":    "\033[90m",
	"blue":         "\033[94m",
	"green":        "\033[92m",
	"aqua":         "\033[96m",
	"red":          "\033[91m",
	"light_purple": "\033[95m",
	"yellow":       "\033[93m",
	"white":        "\033[97m",
}

// Minecraft 传统样式颜色码 (§) 映射
var legacyColorMap = map[rune]string{
	'0': "\033[30m", '1': "\033[34m", '2': "\033[32m", '3': "\033[36m",
	'4': "\033[31m", '5': "\033[35m", '6': "\033[33m", '7': "\033[37m",
	'8': "\033[90m", '9': "\033[94m", 'a': "\033[92m", 'b': "\033[96m",
	'c': "\033[91m", 'd': "\033[95m", 'e': "\033[93m", 'f': "\033[97m",
	'l': "\033[1m", 'o': "\033[3m", 'n': "\033[4m", 'm': "\033[9m", 'r': "\033[0m",
}

const ansiReset = "\033[0m" // ANSI 重置样式

// Windows Console 颜色定义
var windowsConsoleColors = []struct {
	r, g, b uint8
	code    string
}{
	{0, 0, 0, "\033[30m"},       // 黑色
	{128, 0, 0, "\033[31m"},     // 深红
	{0, 128, 0, "\033[32m"},     // 深绿
	{128, 128, 0, "\033[33m"},   // 深黄
	{0, 0, 128, "\033[34m"},     // 深蓝
	{128, 0, 128, "\033[35m"},   // 深紫
	{0, 128, 128, "\033[36m"},   // 深青
	{192, 192, 192, "\033[37m"}, // 灰色
	{128, 128, 128, "\033[90m"}, // 深灰
	{255, 0, 0, "\033[91m"},     // 红色
	{0, 255, 0, "\033[92m"},     // 绿色
	{255, 255, 0, "\033[93m"},   // 黄色
	{0, 0, 255, "\033[94m"},     // 蓝色
	{255, 0, 255, "\033[95m"},   // 紫色
	{0, 255, 255, "\033[96m"},   // 青色
	{255, 255, 255, "\033[97m"}, // 白色
}

// RGB颜色到Windows Console颜色的转换
func rgbToConsoleColor(r, g, b uint8) string {
	minDist := uint32(255*255*3) + 1
	bestCode := "\033[37m" // 默认为灰色

	for _, color := range windowsConsoleColors {
		// 计算RGB距离
		dr := int32(r) - int32(color.r)
		dg := int32(g) - int32(color.g)
		db := int32(b) - int32(color.b)
		dist := uint32(dr*dr + dg*dg + db*db)

		if dist < minDist {
			minDist = dist
			bestCode = color.code
		}
	}

	return bestCode
}

// 将十六进制颜色值转换为 ANSI 颜色代码
func hexToANSI(hex string) string {
	if len(hex) != 7 || hex[0] != '#' {
		return ""
	}
	r, _ := strconv.ParseUint(hex[1:3], 16, 8)
	g, _ := strconv.ParseUint(hex[3:5], 16, 8)
	b, _ := strconv.ParseUint(hex[5:7], 16, 8)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
}

// 获取颜色名称或十六进制颜色的 ANSI 码
func getColorANSI(color string) string {
	if strings.HasPrefix(color, "#") {
		return hexToANSI(color)
	}
	if code, ok := minecraftColorMap[color]; ok {
		return code
	}
	return ""
}

// 解析传统样式颜色字符串（带有 § 符号的）
func parseLegacyColorString(s string) string {
	var builder strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); {
		if runes[i] == '§' && i+1 < len(runes) {
			if code, ok := legacyColorMap[runes[i+1]]; ok {
				builder.WriteString(code)
				i += 2
				continue
			}
		}
		builder.WriteRune(runes[i])
		i++
	}
	builder.WriteString(ansiReset)
	return builder.String()
}

// 递归提取聊天组件中的纯文本内容
func parseChatComponentPlain(component ChatComponent) string {
	var builder strings.Builder
	builder.WriteString(component.Text)
	for _, child := range component.Extra {
		if child.TextComponent != nil {
			builder.WriteString(parseChatComponentPlain(*child.TextComponent))
		} else {
			builder.WriteString(child.RawString)
		}
	}
	return builder.String()
}

// 递归提取并着色聊天组件内容
func parseChatComponentColored(component ChatComponent) string {
	var builder strings.Builder
	colorCode := getColorANSI(component.Color)
	if colorCode != "" {
		builder.WriteString(colorCode)
	}
	builder.WriteString(parseLegacyColorString(component.Text))
	for _, child := range component.Extra {
		if child.TextComponent != nil {
			builder.WriteString(parseChatComponentColored(*child.TextComponent))
		} else {
			builder.WriteString(parseLegacyColorString(child.RawString))
		}
	}
	builder.WriteString(ansiReset)
	return builder.String()
}

// 写入 VarInt 编码（Minecraft 协议所用）
func writeVarInt(buf *bytes.Buffer, value int) {
	for {
		temp := byte(value & 0x7F)
		value >>= 7
		if value != 0 {
			temp |= 0x80
		}
		buf.WriteByte(temp)
		if value == 0 {
			break
		}
	}
}

// 读取 VarInt 编码
func readVarInt(r io.Reader) (int, error) {
	var num, numRead int
	for {
		b := make([]byte, 1)
		_, err := r.Read(b)
		if err != nil {
			return 0, err
		}
		val := b[0] & 0x7F
		num |= int(val) << (7 * numRead)
		if b[0]&0x80 == 0 {
			break
		}
		numRead++
		if numRead > 5 {
			return 0, fmt.Errorf("VarInt 太长")
		}
	}
	return num, nil
}

// 建立连接并获取服务器状态 JSON 与响应延迟
func getServerStatus(host string, port uint16) (string, time.Duration, error) {
	address := fmt.Sprintf("[%s]:%d", host, port)
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		return "", 0, err
	}
	defer conn.Close()

	// 构建握手数据包
	var handshake bytes.Buffer
	handshake.WriteByte(0x00)
	writeVarInt(&handshake, 754) // 协议版本
	writeVarInt(&handshake, len(host))
	handshake.WriteString(host)
	binary.Write(&handshake, binary.BigEndian, port)
	writeVarInt(&handshake, 1) // 状态请求

	// 发送握手包
	var packet bytes.Buffer
	writeVarInt(&packet, handshake.Len())
	packet.Write(handshake.Bytes())
	conn.Write(packet.Bytes())

	// 发送状态请求
	conn.Write([]byte{0x01, 0x00})

	// 计时开始
	start := time.Now()

	// 接收响应
	length, err := readVarInt(conn)
	if err != nil {
		return "", 0, err
	}
	data := make([]byte, length)
	_, err = io.ReadFull(conn, data)
	if err != nil {
		return "", 0, err
	}
	ping := time.Since(start)

	dataBuf := bytes.NewBuffer(data)
	_, _ = readVarInt(dataBuf)        // 丢弃 Packet ID
	jsonLen, _ := readVarInt(dataBuf) // 读取 JSON 长度

	jsonData := make([]byte, jsonLen)
	_, err = io.ReadFull(dataBuf, jsonData)
	if err != nil {
		return "", 0, err
	}

	return string(jsonData), ping, nil
}

// 将域名解析为 IP 地址
func resolveHostToIP(host string) string {
	ips, err := net.LookupHost(host)
	if err != nil {
		return "无法解析 IP 地址"
	}
	return ips[0]
}

func main() {
	var (
		debug     bool
		showColor bool
		showText  bool
	)

	// 定义命令行参数
	flag.BoolVar(&debug, "debug", false, "显示全部 MOTD 信息（包括原始 JSON、彩色样式、纯文本）")
	flag.BoolVar(&showColor, "color", false, "")
	flag.BoolVar(&showColor, "c", false, "")
	flag.BoolVar(&showText, "text", false, "")
	flag.BoolVar(&showText, "t", false, "")

	// 自定义帮助信息
	flag.Usage = func() {
		fmt.Println("用法:")
		fmt.Println("    motd [选项] <地址>[:端口]")
		fmt.Println("    (如未指定端口，默认使用 25565)")
		fmt.Println("")
		fmt.Println("选项:")
		fmt.Println("    --debug           显示全部 MOTD 信息(包括原始 JSON、彩色样式、纯文本)")
		fmt.Println("    -c, --color       显示彩色 MOTD 样式(默认)")
		fmt.Println("    -t, --text        显示纯文本 MOTD 样式(适合老旧终端)")
		fmt.Println("    -h, --help        显示此帮助信息")
		fmt.Println("")
		fmt.Println("示例:")
		fmt.Println("    motd mc.example.com:25565")
		fmt.Println("    motd --debug mc.example.com")
		fmt.Println("    motd -t mc.example.com")
		fmt.Println("")
		fmt.Println("关于:")
		fmt.Println("    minecraft-je-motd")
		fmt.Println("    版本: 1.0.3-HTTPS-DNS")
		fmt.Println("    作者: kcraftnetwork {YF_Eternal + kakcraft}")
		fmt.Println("    Github: https://github.com/YF-Eternal/minecraft-je-motd/")
	}

	// 处理 --help 参数
	for _, arg := range os.Args {
		if arg == "--help" {
			flag.Usage()
			os.Exit(0)
		}
	}

	flag.Parse()

	if len(flag.Args()) < 1 {
		flag.Usage()
		os.Exit(1)
	}

	addr := flag.Args()[0]
	parts := strings.Split(addr, ":")
	host := parts[0]
	var port uint16 = 25565

	// 如果指定了端口，进行解析
	if len(parts) == 2 {
		p, err := strconv.ParseUint(parts[1], 10, 16)
		if err != nil {
			fmt.Println("无效的端口:", err)
			os.Exit(1)
		}
		port = uint16(p)
	}

	ip := resolveHostToIP(host)
	fmt.Printf("正在尝试获取 %s [%s:%d] 的 MOTD 信息...\n", host, ip, port)

	jsonStr, ping, err := getServerStatus(host, uint16(port))
	if err != nil {
		fmt.Println("\n无法连接到服务器:", err)
		os.Exit(1)
	}

	// 解析服务器状态 JSON
	var data struct {
		Version struct {
			Name     string `json:"name"`
			Protocol int    `json:"protocol"`
		} `json:"version"`
		Players struct {
			Online int `json:"online"`
			Max    int `json:"max"`
		} `json:"players"`
		Description interface{} `json:"description"`
	}
	err = json.Unmarshal([]byte(jsonStr), &data)
	if err != nil {
		fmt.Println("JSON 解析失败:", err)
		os.Exit(1)
	}

	// 提前打印原始 JSON（debug 模式下）
	if debug {
		fmt.Println("\n原始 JSON 数据:")
		fmt.Println(jsonStr)
	}

	// 解析并显示 MOTD 描述信息
	switch desc := data.Description.(type) {
	case map[string]interface{}:
		// JSON 对象类型
		var description ChatComponent
		descJson, _ := json.Marshal(desc)
		if err := json.Unmarshal(descJson, &description); err != nil {
			fmt.Println("描述内容解析失败:", err)
			os.Exit(1)
		}
		if debug {
			fmt.Println("\n纯文本 MOTD:")
			fmt.Println(parseChatComponentPlain(description))
			fmt.Println("\n彩色 MOTD:")
			fmt.Println(parseChatComponentColored(description))
		} else if showText {
			fmt.Println("\n" + parseChatComponentPlain(description))
		} else {
			fmt.Println("\n" + parseChatComponentColored(description))
		}

	case string:
		// 字符串类型（带 § 的旧版）
		if debug {
			fmt.Println("\n纯文本 MOTD:")
			fmt.Println(desc)
			fmt.Println("\n彩色 MOTD:")
			fmt.Println(parseLegacyColorString(desc))
		} else if showText {
			fmt.Println("\n" + desc)
		} else {
			fmt.Println("\n" + parseLegacyColorString(desc))
		}

	default:
		fmt.Println("未知描述格式，跳过 MOTD 解析。")
	}

	// 显示服务器基本信息
	fmt.Printf("\n版本: %s (协议号: %d)\n", data.Version.Name, data.Version.Protocol)
	fmt.Printf("玩家: %d / %d\n", data.Players.Online, data.Players.Max)
	fmt.Printf("延迟: %dms\n", ping.Milliseconds())
}
