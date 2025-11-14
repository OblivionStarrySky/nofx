package market

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	baseURL = "https://fapi.binance.com"
)

type APIClient struct {
	client *http.Client
}

func NewAPIClient() *APIClient {
	// 创建带代理的HTTP客户端
	proxyURL, err := url.Parse("http://127.0.0.1:7898")
	if err != nil {
		log.Printf("⚠️ 代理URL解析失败: %v", err)
	}

	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	return &APIClient{
		client: client,
	}
}

func (c *APIClient) GetExchangeInfo() (*ExchangeInfo, error) {
	url := fmt.Sprintf("%s/fapi/v1/exchangeInfo", baseURL)
	resp, err := c.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var exchangeInfo ExchangeInfo
	err = json.Unmarshal(body, &exchangeInfo)
	if err != nil {
		return nil, err
	}

	return &exchangeInfo, nil
}

func (c *APIClient) GetKlines(symbol, interval string, limit int) ([]Kline, error) {
	url := fmt.Sprintf("%s/fapi/v1/klines", baseURL)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	q := req.URL.Query()
	q.Add("symbol", symbol)
	q.Add("interval", interval)
	q.Add("limit", strconv.Itoa(limit))
	req.URL.RawQuery = q.Encode()

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var klineResponses []KlineResponse
	err = json.Unmarshal(body, &klineResponses)
	if err != nil {
		log.Printf("获取K线数据失败,响应内容: %s", string(body))
		return nil, err
	}

	var klines []Kline
	for _, kr := range klineResponses {
		kline, err := parseKline(kr)
		if err != nil {
			log.Printf("解析K线数据失败: %v", err)
			continue
		}
		klines = append(klines, kline)
	}

	return klines, nil
}

func parseKline(kr KlineResponse) (Kline, error) {
	var kline Kline

	if len(kr) < 11 {
		return kline, fmt.Errorf("invalid kline data")
	}

	// 解析各个字段
	kline.OpenTime = int64(kr[0].(float64))
	kline.Open, _ = strconv.ParseFloat(kr[1].(string), 64)
	kline.High, _ = strconv.ParseFloat(kr[2].(string), 64)
	kline.Low, _ = strconv.ParseFloat(kr[3].(string), 64)
	kline.Close, _ = strconv.ParseFloat(kr[4].(string), 64)
	kline.Volume, _ = strconv.ParseFloat(kr[5].(string), 64)
	kline.CloseTime = int64(kr[6].(float64))
	kline.QuoteVolume, _ = strconv.ParseFloat(kr[7].(string), 64)
	kline.Trades = int(kr[8].(float64))
	kline.TakerBuyBaseVolume, _ = strconv.ParseFloat(kr[9].(string), 64)
	kline.TakerBuyQuoteVolume, _ = strconv.ParseFloat(kr[10].(string), 64)

	return kline, nil
}

func (c *APIClient) GetCurrentPrice(symbol string) (float64, error) {
	url := fmt.Sprintf("%s/fapi/v1/ticker/price", baseURL)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}

	q := req.URL.Query()
	q.Add("symbol", symbol)
	req.URL.RawQuery = q.Encode()

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var ticker PriceTicker
	err = json.Unmarshal(body, &ticker)
	if err != nil {
		return 0, err
	}

	price, err := strconv.ParseFloat(ticker.Price, 64)
	if err != nil {
		return 0, err
	}

	return price, nil
}

// GetOrderBook 获取订单簿数据
func (c *APIClient) GetOrderBook(symbol string, limit int) (*OrderBook, error) {
	url := fmt.Sprintf("%s/fapi/v1/depth", baseURL)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	q := req.URL.Query()
	q.Add("symbol", symbol)
	q.Add("limit", strconv.Itoa(limit))
	req.URL.RawQuery = q.Encode()

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var orderBook OrderBook
	err = json.Unmarshal(body, &orderBook)
	if err != nil {
		return nil, err
	}

	return &orderBook, nil
}

// CalculateDOM 计算DOM(订单簿深度)指标
func (c *APIClient) CalculateDOM(symbol string) (DOMData, error) {
	var dom DOMData

	// 获取订单簿数据，限制为1000档位
	orderBook, err := c.GetOrderBook(symbol, 1000)
	if err != nil {
		return dom, fmt.Errorf("获取订单簿数据失败: %v", err)
	}

	// 计算买单深度(前10档)
	bidDepth := 0.0
	askDepth := 0.0

	// 计算买单深度(最高价前10档)
	bidLevels := len(orderBook.Bids)
	if bidLevels > 10 {
		bidLevels = 10
	}

	for i := 0; i < bidLevels; i++ {
		if len(orderBook.Bids[i]) >= 2 {
			quantity, _ := strconv.ParseFloat(orderBook.Bids[i][1].(string), 64)
			bidDepth += quantity
		}
	}

	// 计算卖单深度(最低价前10档)
	askLevels := len(orderBook.Asks)
	if askLevels > 10 {
		askLevels = 10
	}

	for i := 0; i < askLevels; i++ {
		if len(orderBook.Asks[i]) >= 2 {
			quantity, _ := strconv.ParseFloat(orderBook.Asks[i][1].(string), 64)
			askDepth += quantity
		}
	}

	dom.BidDepth = bidDepth
	dom.AskDepth = askDepth

	// 计算深度比率
	if askDepth != 0 {
		dom.DepthRatio = bidDepth / askDepth
	} else {
		dom.DepthRatio = 0
	}

	return dom, nil
}
