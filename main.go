package main

import (
	"fmt"

	"github.com/go-resty/resty/v2"
)

var client = resty.New()

func main() {
	symbol := "0.000001"

	resp, err := client.R().
		SetQueryParams(map[string]string{
			"fields1": "f1,f2,f3,f4",
			"fields2": "f51,f52,f53,f54,f55",
			"mpi":     "2000",
			"ut":      "bd1d9ddb04089700cf9c27f6f7426281",
			"fltt":    "2",
			"pos":     "-0",
			"secid":   symbol,
			"wbp2u":   "|0|0|0|web",
		}).Get("https://70.push2.eastmoney.com/api/qt/stock/details/sse")
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("%s\n", resp)
}
