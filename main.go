package main

import (
	"fmt"
	"net/url"

	"github.com/r3labs/sse/v2"
)

func main() {
	symbol := "0.000001"
	baseUrl := "https://70.push2.eastmoney.com/api/qt/stock/details/sse"
	params := map[string]string{
		"fields1": "f1,f2,f3,f4",
		"fields2": "f51,f52,f53,f54,f55",
		"mpi":     "2000",
		"ut":      "bd1d9ddb04089700cf9c27f6f7426281",
		"fltt":    "2",
		"pos":     "-0",
		"secid":   symbol,
		"wbp2u":   "|0|0|0|web",
	}

	//events := make(chan *sse.Event, 100)

	url := urlWithParams(baseUrl, params)
	fmt.Println(url)
	client := sse.NewClient(url)

	err := client.SubscribeRaw(func(msg *sse.Event) {
		// Got some data!
		fmt.Printf("%s", msg.Data)
	})
	if err != nil {
		fmt.Println(err)
	}
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
