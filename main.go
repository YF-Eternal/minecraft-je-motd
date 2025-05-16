// 作者: YF_Eternal, kaiserverkcraft
// 项目: minecraft-je-motd
// 版本: 1.0.3
// 许可: MIT
// 描述: 一个命令行工具, 用于获取并展示 Minecraft Java 版服务器的 MOTD 信息。
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
	"os"
	"strconv"
	"strings"
	"time"
)

// ChatComponent 表示聊天组件结构体 (用于 JSON 解析)
type ChatComponent struct {
	Text  string               `json:"text,omitempty"`  // 文本内容
	Color string               `json:"color,omitempty"` // 文本颜色
	Extra []ChatComponentMixed `json:"extra,omitempty"` // 嵌套组件
}

// 聊天组件的多种可能格式 (组件或纯字符串)
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

// 解析传统样式颜色字符串 (带有 § 符号的)
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

// 写入 VarInt 编码 (Minecraft 协议所用)
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
func getServerStatus(host string, port uint16, timeout time.Duration) (string, time.Duration, error) {
	address := fmt.Sprintf("[%s]:%d", host, port)

	var conn net.Conn
	var err error
	if timeout == 0 {
		conn, err = net.Dial("tcp", address)
	} else {
		conn, err = net.DialTimeout("tcp", address, timeout)
	}
	if err != nil {
		return "", 0, err
	}
	defer conn.Close()

	if timeout > 0 {
		conn.SetDeadline(time.Now().Add(timeout))
	}

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
	_, err = conn.Write(packet.Bytes())
	if err != nil {
		return "", 0, err
	}

	// 发送状态请求
	_, err = conn.Write([]byte{0x01, 0x00})
	if err != nil {
		return "", 0, err
	}

	// 读取服务器状态 JSON
	length, err := readVarInt(conn)
	if err != nil {
		return "", 0, err
	}
	data := make([]byte, length)
	_, err = io.ReadFull(conn, data)
	if err != nil {
		return "", 0, err
	}

	dataBuf := bytes.NewBuffer(data)
	_, _ = readVarInt(dataBuf)        // 丢弃 Packet ID
	jsonLen, _ := readVarInt(dataBuf) // 读取 JSON 长度

	jsonData := make([]byte, jsonLen)
	_, err = io.ReadFull(dataBuf, jsonData)
	if err != nil {
		return "", 0, err
	}

	// 纯网络延迟ping测量开始
	start := time.Now()

	var pingPacket bytes.Buffer
	writeVarInt(&pingPacket, 9)                                                   // 包长度 1字节包ID + 8字节时间戳 = 9
	pingPacket.WriteByte(0x01)                                                    // 包 ID Ping
	binary.Write(&pingPacket, binary.BigEndian, int64(time.Now().UnixNano()/1e6)) // 时间戳 (毫秒)

	_, err = conn.Write(pingPacket.Bytes())
	if err != nil {
		return "", 0, err
	}

	// 读取 pong 包
	_, err = readVarInt(conn) // 读取包长度
	if err != nil {
		return "", 0, err
	}

	packetID, err := readVarInt(conn) // 读取包ID
	if err != nil {
		return "", 0, err
	}

	if packetID != 0x01 {
		return "", 0, fmt.Errorf("ping 响应包 ID 错误, 收到 ID %d", packetID)
	}

	// 读取 pong 时间戳 (8字节)
	var pongTime int64
	err = binary.Read(conn, binary.BigEndian, &pongTime)
	if err != nil {
		return "", 0, err
	}

	ping := time.Since(start)

	// 返回状态 JSON 和 ping 延迟
	return string(jsonData), ping, nil
}

// 将域名解析为 IP 地址
func resolveHostToIP(host string) string {
	ips, err := net.LookupHost(host)
	if err != nil || len(ips) == 0 {
		return "无法解析 IP 地址"
	}
	return ips[0]
}

// 尝试解析 Minecraft 的 SRV 记录获取实际主机名与端口
func resolveMinecraftSRV(name string) (host string, port uint16, err error) {
	_, addrs, err := net.LookupSRV("minecraft", "tcp", name)
	if err != nil || len(addrs) == 0 {
		return name, 25565, nil // 无 SRV 记录时使用默认端口
	}
	return strings.TrimSuffix(addrs[0].Target, "."), addrs[0].Port, nil
}

func main() {
	var debug, showColor, showText bool
	var timeout int

	flag.BoolVar(&debug, "debug", false, "显示全部 MOTD 信息")
	flag.BoolVar(&showColor, "color", false, "")
	flag.BoolVar(&showColor, "c", false, "")
	flag.BoolVar(&showText, "plain", false, "")
	flag.BoolVar(&showText, "p", false, "")
	flag.IntVar(&timeout, "timeout", 5, "设置连接超时秒数 (0 表示直到 TCP 超时)")
	flag.IntVar(&timeout, "t", 5, "设置连接超时秒数 (0 表示直到 TCP 超时)")
	flag.Usage = func() {
		fmt.Println("用法:")
		fmt.Println("    motd [选项] <地址>[:端口]")
		fmt.Println("    (如未指定端口，默认使用 25565)")
		fmt.Println("")
		fmt.Println("选项:")
		fmt.Println("    --debug           显示全部 MOTD 信息(包括原始 JSON、彩色样式、纯文本)")
		fmt.Println("    -c, --color       显示彩色 MOTD 样式(默认)")
		fmt.Println("    -p, --plain       显示纯文本 MOTD 样式(适合老旧终端)")
		fmt.Println("    -t, --timeout     设置连接超时等待时间 (默认: 5s, 输入 0 表示直到 TCP 连接超时)")
		fmt.Println("    -h, --help        显示此帮助信息")
		fmt.Println("")
		fmt.Println("示例:")
		fmt.Println("    motd mc.example.com:25565")
		fmt.Println("    motd --debug mc.example.com")
		fmt.Println("    motd -t 3 mc.example.com")
		fmt.Println("")
		fmt.Println("关于:")
		fmt.Println("    minecraft-je-motd")
		fmt.Println("    版本: 1.0.3")
		fmt.Println("    作者: YF_Eternal, kaiserverkcraft")
		fmt.Println("    Github: https://github.com/YF-Eternal/minecraft-je-motd/")
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
	} else {
		// 未指定端口，尝试解析 SRV 记录
		srvHost, srvPort, err := resolveMinecraftSRV(host)
		if err == nil {
			host = srvHost
			port = srvPort
		}
	}

	ip := resolveHostToIP(host)
	fmt.Printf("正在尝试获取 %s [%s:%d] 的 MOTD 信息...\n", host, ip, port)

	jsonStr, ping, err := getServerStatus(host, port, time.Duration(timeout)*time.Second)
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

	// 提前打印原始 JSON (debug 模式下)
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
			fmt.Println("描述解析失败:", err)
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
		// 字符串类型 (带 § 的旧版)
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
		fmt.Println("未知的描述格式")
	}

	// 显示服务器基本信息
	fmt.Printf("\n服务端: %s | 协议: %d\n", data.Version.Name, data.Version.Protocol)
	fmt.Printf("在线人数: %d / %d\n", data.Players.Online, data.Players.Max)
	fmt.Printf("Ping 延迟: %dms\n", ping.Milliseconds())
}
