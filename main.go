// Author: YF_Eternal
// Project: minecraft-je-motd
// Version: 1.0.0
// License: MIT
// Description: A command-line tool to fetch and display Minecraft Java Edition server MOTD.
// Github: https://github.com/YF-Eternal/minecraft-je-motd/

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

type ChatComponent struct {
	Text  string               `json:"text,omitempty"`
	Color string               `json:"color,omitempty"`
	Extra []ChatComponentMixed `json:"extra,omitempty"`
}

type ChatComponentMixed struct {
	TextComponent *ChatComponent
	RawString     string
}

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

var legacyColorMap = map[rune]string{
	'0': "\033[30m", '1': "\033[34m", '2': "\033[32m", '3': "\033[36m",
	'4': "\033[31m", '5': "\033[35m", '6': "\033[33m", '7': "\033[37m",
	'8': "\033[90m", '9': "\033[94m", 'a': "\033[92m", 'b': "\033[96m",
	'c': "\033[91m", 'd': "\033[95m", 'e': "\033[93m", 'f': "\033[97m",
	'l': "\033[1m", 'o': "\033[3m", 'n': "\033[4m", 'm': "\033[9m", 'r': "\033[0m",
}

const ansiReset = "\033[0m"

func hexToANSI(hex string) string {
	if len(hex) != 7 || hex[0] != '#' {
		return ""
	}
	r, _ := strconv.ParseUint(hex[1:3], 16, 8)
	g, _ := strconv.ParseUint(hex[3:5], 16, 8)
	b, _ := strconv.ParseUint(hex[5:7], 16, 8)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
}

func getColorANSI(color string) string {
	if strings.HasPrefix(color, "#") {
		return hexToANSI(color)
	}
	if code, ok := minecraftColorMap[color]; ok {
		return code
	}
	return ""
}

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

func getServerStatus(host string, port uint16) (string, time.Duration, error) {
	address := fmt.Sprintf("[%s]:%d", host, port)
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		return "", 0, err
	}
	defer conn.Close()

	var handshake bytes.Buffer
	handshake.WriteByte(0x00)
	writeVarInt(&handshake, 754) // Protocol version
	writeVarInt(&handshake, len(host))
	handshake.WriteString(host)
	binary.Write(&handshake, binary.BigEndian, port)
	writeVarInt(&handshake, 1) // Status

	var packet bytes.Buffer
	writeVarInt(&packet, handshake.Len())
	packet.Write(handshake.Bytes())
	conn.Write(packet.Bytes())

	// 发送状态请求
	conn.Write([]byte{0x01, 0x00})

	// 开始计时
	start := time.Now()

	// 读取响应
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
	_, _ = readVarInt(dataBuf)        // Packet ID
	jsonLen, _ := readVarInt(dataBuf) // JSON length

	jsonData := make([]byte, jsonLen)
	_, err = io.ReadFull(dataBuf, jsonData)
	if err != nil {
		return "", 0, err
	}

	return string(jsonData), ping, nil
}

func resolveHostToIP(host string) string {
	ips, err := net.LookupHost(host)
	if err != nil {
		return "无法解析 IP 地址"
	}
	return ips[0] // 返回第一个解析到的 IP 地址
}

func main() {
	var (
		debug     bool
		showColor bool
		showText  bool
	)

	flag.BoolVar(&debug, "debug", false, "显示全部 MOTD 信息（包括原始 JSON、彩色样式、纯文本）")
	flag.BoolVar(&showColor, "color", false, "")
	flag.BoolVar(&showColor, "c", false, "")
	flag.BoolVar(&showText, "text", false, "")
	flag.BoolVar(&showText, "t", false, "")

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
		fmt.Println("    版本: 1.0.0")
		fmt.Println("    作者: YF_Eternal")
		fmt.Println("    Github: https://github.com/YF-Eternal/minecraft-je-motd/")
	}

	// 支持 --help 手动触发
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
	if len(parts) == 1 {
		parts = append(parts, "25565") // 默认端口
	}
	host := parts[0]
	port, err := strconv.ParseUint(parts[1], 10, 16)
	if err != nil {
		fmt.Println("无效的端口:", err)
		os.Exit(1)
	}

	ip := resolveHostToIP(host)
	fmt.Printf("正在尝试获取 %s [%s:%d] 的 MOTD 信息...\n", host, ip, port)

	jsonStr, ping, err := getServerStatus(host, uint16(port))
	if err != nil {
		fmt.Println("连接错误:", err)
		os.Exit(1)
	}

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

	// 输出描述
	if descComponent, ok := data.Description.(map[string]interface{}); ok {
		var description ChatComponent
		descJson, _ := json.Marshal(descComponent)
		err := json.Unmarshal(descJson, &description)
		if err != nil {
			fmt.Println("描述内容解析失败:", err)
			os.Exit(1)
		}

		if debug {
			fmt.Println("\n原始 JSON 数据:")
			fmt.Println(jsonStr)
			fmt.Println("\n纯文本 MOTD:")
			fmt.Println(parseChatComponentPlain(description))
			fmt.Println("\n彩色 MOTD:")
			fmt.Println(parseChatComponentColored(description))
		} else if showText {
			fmt.Println("\n" + parseChatComponentPlain(description))
		} else {
			fmt.Println("\n" + parseChatComponentColored(description))
		}

	} else if strDesc, ok := data.Description.(string); ok {
		if showText || debug {
			fmt.Println("\n" + parseLegacyColorString(strDesc))
		} else {
			fmt.Println("\n" + parseLegacyColorString(strDesc))
		}
	}

	fmt.Printf("\n版本: %s (协议号: %d)\n", data.Version.Name, data.Version.Protocol)
	fmt.Printf("玩家: %d / %d\n", data.Players.Online, data.Players.Max)
	fmt.Printf("延迟: %dms\n", ping.Milliseconds())
}
