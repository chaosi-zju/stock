package main

import (
	"fmt"
	"hash/crc32"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/peterhellberg/sseclient"
)

const (
	intraDayBaseUrlEM = "https://70.push2.eastmoney.com/api/qt/stock/details/sse"
)

var (
	intraDayBaseParamEM = map[string]string{
		"fields1": "f1,f2,f3,f4",
		"fields2": "f51,f52,f53,f54,f55",
		"mpi":     "2000",
		"ut":      "bd1d9ddb04089700cf9c27f6f7426281",
		"fltt":    "2",
		"pos":     "-0",
		"wbp2u":   "|0|0|0|web",
		//"secid":   "0.159915",
	}
)

type IntraDay struct {
	Uid   string
	Time  time.Time
	Price float64
	Hand  int
	Kind  int
}

func main() {
	intraDayData := make([]IntraDay, 0)
	go watchIntraDay(159915, intraDayData)

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	fmt.Printf("listening for signals ...\n")
	select {
	case <-signalCh:
		fmt.Printf("signal received, gracefully shutdown...\n")
	}
}

func watchIntraDay(code int64, intraDayData []IntraDay) {
	intraDayBaseParamEM["secid"] = formatCodeEM(code)
	intraDayUrl := urlWithParams(intraDayBaseUrlEM, intraDayBaseParamEM)

	events, err := sseclient.OpenURL(intraDayUrl)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}

	for event := range events {
		if len(event.Data) == 0 {
			continue
		}
		if event.Data["data"] == nil {
			continue
		}
		data, ok := event.Data["data"].(map[string]interface{})
		if !ok {
			continue
		}
		details, ok := data["details"].([]interface{})
		if !ok || len(details) == 0 {
			continue
		}
		for _, item := range details {
			e := item.(string)
			ary := strings.Split(e, ",")
			if len(ary) < 5 {
				fmt.Printf("invalid event: %s\n", item)
				continue
			}

			intraDay := IntraDay{}
			intraDay.Uid = fmt.Sprintf("%d-%d", crc32.ChecksumIEEE([]byte(time.Now().Format(time.DateOnly))), len(intraDayData))
			intraDay.Time = atot(time.Now().Format(time.DateOnly), ary[0])
			intraDay.Price = atof(ary[1])
			intraDay.Hand = atoi(ary[2])
			intraDay.Kind = buySellKind(ary[4])
			fmt.Printf("%+v\n", intraDay)
			intraDayData = append(intraDayData, intraDay)
		}
		if len(intraDayData) == 4812 {
			break
		}
	}

	buy, sell := 0, 0
	for _, r := range intraDayData {
		if r.Kind > 0 {
			buy += r.Hand
		} else if r.Kind < 0 {
			sell += r.Hand
		}
	}
	fmt.Println(buy)
	fmt.Println(sell)
}

func urlWithParams(baseUrl string, params map[string]string) string {
	base, err := url.Parse(baseUrl)
	if err != nil {
		fmt.Println(err)
		return baseUrl
	}

	values := url.Values{}
	for k, v := range params {
		values.Add(k, v)
	}
	base.RawQuery = values.Encode()

	return base.String()
}

func formatCodeEM(code int64) string {
	firstBit := code / 10
	if firstBit == 5 {
		return fmt.Sprintf("1.%d", code)
	}
	return fmt.Sprintf("0.%d", code)
}

func buySellKind(kind string) int {
	switch kind {
	case "2":
		// 买盘
		return 1
	case "1":
		// 卖盘
		return -1
	default:
		// 4 中性盘
		return 0
	}
}

func atoi(value string) int {
	val, err := strconv.Atoi(value)
	if err != nil {
		fmt.Printf("%+v\n", err)
	}
	return val
}

func atof(value string) float64 {
	val, err := strconv.ParseFloat(value, 64)
	if err != nil {
		fmt.Printf("%+v\n", err)
	}
	return val
}

func atot(dateStr, timeStr string) time.Time {
	t, err := time.Parse(time.DateTime, dateStr+" "+timeStr)
	if err != nil {
		fmt.Printf("%+v\n", err)
	}
	return t
}
