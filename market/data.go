package market

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FundingRateCache 资金费率缓存结构
// Binance Funding Rate 每 8 小时才更新一次，使用 1 小时缓存可显著减少 API 调用
type FundingRateCache struct {
	Rate      float64
	UpdatedAt time.Time
}

var (
	fundingRateMap sync.Map // map[string]*FundingRateCache
	frCacheTTL     = 1 * time.Hour
)

// Get 获取指定代币的市场数据
func Get(symbol string) (*Data, error) {
	var klines15m, klines4h []Kline
	var err error
	// 标准化symbol
	symbol = Normalize(symbol)
	// 获取15分钟K线数据 (最近10个)
	klines15m, err = WSMonitorCli.GetCurrentKlines(symbol, "15m") // 多获取一些用于计算
	if err != nil {
		return nil, fmt.Errorf("获取15分钟K线失败: %v", err)
	}

	// 获取4小时K线数据 (最近10个)
	klines4h, err = WSMonitorCli.GetCurrentKlines(symbol, "4h") // 多获取用于计算指标
	if err != nil {
		return nil, fmt.Errorf("获取4小时K线失败: %v", err)
	}

	// 检查数据是否为空
	if len(klines15m) == 0 {
		return nil, fmt.Errorf("15分钟K线数据为空")
	}
	if len(klines4h) == 0 {
		return nil, fmt.Errorf("4小时K线数据为空")
	}

	// 计算当前指标 (基于15分钟最新数据)
	currentPrice := klines15m[len(klines15m)-1].Close
	currentEMA20 := calculateEMA(klines15m, 20)
	currentMACD := calculateMACD(klines15m)
	currentRSI7 := calculateRSI(klines15m, 7)
	// 计算KDJ指标
	currentKDJ := calculateKDJ(klines15m, 9)
	// 计算DMI指标
	currentDMI := calculateDMI(klines15m, 14)

	// 计算DOM指标
	apiClient := NewAPIClient()
	domData, err := apiClient.CalculateDOM(symbol)
	if err != nil {
		// DOM计算失败不影响整体,使用默认值
		domData = DOMData{BidDepth: 0, AskDepth: 0, DepthRatio: 0}
		fmt.Printf("⚠️ 获取 %s 的DOM数据失败: %v\n", symbol, err)
	}

	// 计算4小时时间框架的指标
	hourlyEMA20 := calculateEMA(klines4h, 20)
	hourlyMACD := calculateMACD(klines4h)
	hourlyRSI7 := calculateRSI(klines4h, 7)
	hourlyRSI14 := calculateRSI(klines4h, 14)
	hourlyKDJ := calculateKDJ(klines4h, 9)
	hourlyDMI := calculateDMI(klines4h, 14)

	// 计算价格变化百分比
	// 1小时价格变化 = 4个15分钟K线前的价格
	priceChange1h := 0.0
	if len(klines15m) >= 5 { // 至少需要5根K线 (当前 + 4根前)
		price1hAgo := klines15m[len(klines15m)-5].Close
		if price1hAgo > 0 {
			priceChange1h = ((currentPrice - price1hAgo) / price1hAgo) * 100
		}
	}

	// 4小时价格变化 = 1个4小时K线前的价格
	priceChange4h := 0.0
	if len(klines4h) >= 2 {
		price4hAgo := klines4h[len(klines4h)-2].Close
		if price4hAgo > 0 {
			priceChange4h = ((currentPrice - price4hAgo) / price4hAgo) * 100
		}
	}

	// 获取OI数据
	oiData, err := getOpenInterestData(symbol)
	if err != nil {
		// OI失败不影响整体,使用默认值
		oiData = &OIData{Latest: 0, Average: 0}
		// 打印错误日志以便调试
		fmt.Printf("⚠️ 获取 %s 的OI数据失败: %v\n", symbol, err)
	}

	// 获取Funding Rate
	fundingRate, _ := getFundingRate(symbol)

	// 计算日内系列数据
	intradayData := calculateIntradaySeries(klines15m)

	// 计算长期数据
	longerTermData := calculateLongerTermData(klines4h)

	return &Data{
		Symbol:            symbol,
		CurrentPrice:      currentPrice,
		PriceChange1h:     priceChange1h,
		PriceChange4h:     priceChange4h,
		CurrentEMA20:      currentEMA20,
		CurrentMACD:       currentMACD,
		CurrentRSI7:       currentRSI7,
		CurrentKDJ:        currentKDJ,
		CurrentDMI:        currentDMI,
		DOMData:           domData,
		HourlyEMA20:       hourlyEMA20,
		HourlyMACD:        hourlyMACD,
		HourlyRSI7:        hourlyRSI7,
		HourlyRSI14:       hourlyRSI14,
		HourlyKDJ:         hourlyKDJ,
		HourlyDMI:         hourlyDMI,
		OpenInterest:      oiData,
		FundingRate:       fundingRate,
		IntradaySeries:    intradayData,
		LongerTermContext: longerTermData,
	}, nil
}

// calculateEMA 计算EMA
func calculateEMA(klines []Kline, period int) float64 {
	if len(klines) < period {
		return 0
	}

	// 计算SMA作为初始EMA
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += klines[i].Close
	}
	ema := sum / float64(period)

	// 计算EMA
	multiplier := 2.0 / float64(period+1)
	for i := period; i < len(klines); i++ {
		ema = (klines[i].Close-ema)*multiplier + ema
	}

	return ema
}

// calculateMACD 计算MACD
func calculateMACD(klines []Kline) float64 {
	if len(klines) < 26 {
		return 0
	}

	// 计算12期和26期EMA
	ema12 := calculateEMA(klines, 12)
	ema26 := calculateEMA(klines, 26)

	// MACD = EMA12 - EMA26
	return ema12 - ema26
}

// calculateRSI 计算RSI
func calculateRSI(klines []Kline, period int) float64 {
	if len(klines) <= period {
		return 0
	}

	gains := 0.0
	losses := 0.0

	// 计算初始平均涨跌幅
	for i := 1; i <= period; i++ {
		change := klines[i].Close - klines[i-1].Close
		if change > 0 {
			gains += change
		} else {
			losses += -change
		}
	}

	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)

	// 使用Wilder平滑方法计算后续RSI
	for i := period + 1; i < len(klines); i++ {
		change := klines[i].Close - klines[i-1].Close
		if change > 0 {
			avgGain = (avgGain*float64(period-1) + change) / float64(period)
			avgLoss = (avgLoss * float64(period-1)) / float64(period)
		} else {
			avgGain = (avgGain * float64(period-1)) / float64(period)
			avgLoss = (avgLoss*float64(period-1) + (-change)) / float64(period)
		}
	}

	if avgLoss == 0 {
		return 100
	}

	rs := avgGain / avgLoss
	rsi := 100 - (100 / (1 + rs))

	return rsi
}

// calculateATR 计算ATR
func calculateATR(klines []Kline, period int) float64 {
	if len(klines) <= period {
		return 0
	}

	trs := make([]float64, len(klines))
	for i := 1; i < len(klines); i++ {
		high := klines[i].High
		low := klines[i].Low
		prevClose := klines[i-1].Close

		tr1 := high - low
		tr2 := math.Abs(high - prevClose)
		tr3 := math.Abs(low - prevClose)

		trs[i] = math.Max(tr1, math.Max(tr2, tr3))
	}

	// 计算初始ATR
	sum := 0.0
	for i := 1; i <= period; i++ {
		sum += trs[i]
	}
	atr := sum / float64(period)

	// Wilder平滑
	for i := period + 1; i < len(klines); i++ {
		atr = (atr*float64(period-1) + trs[i]) / float64(period)
	}

	return atr
}

// calculateDM 计算方向运动值
func calculateDM(klines []Kline) ([]float64, []float64) {
	plusDM := make([]float64, len(klines))
	minusDM := make([]float64, len(klines))

	for i := 1; i < len(klines); i++ {
		upMove := klines[i].High - klines[i-1].High
		downMove := klines[i-1].Low - klines[i].Low

		if upMove > downMove && upMove > 0 {
			plusDM[i] = upMove
		} else {
			plusDM[i] = 0
		}

		if downMove > upMove && downMove > 0 {
			minusDM[i] = downMove
		} else {
			minusDM[i] = 0
		}
	}

	return plusDM, minusDM
}

// calculateDI 计算方向指标
func calculateDI(dm []float64, tr []float64, period int) []float64 {
	di := make([]float64, len(dm))

	// 计算初始DM和TR的平均值
	sumDM := 0.0
	sumTR := 0.0

	for i := 1; i <= period; i++ {
		sumDM += dm[i]
		sumTR += tr[i]
	}

	// Wilder平滑
	if sumTR != 0 {
		di[period] = (sumDM / sumTR) * 100
	}

	for i := period + 1; i < len(dm); i++ {
		di[i] = ((di[i-1]*float64(period-1) + dm[i]) / float64(period)) * 100
		if tr[i] != 0 {
			di[i] = di[i] / tr[i] * 100
		}
	}

	return di
}

// calculateDMI 计算DMI指标
func calculateDMI(klines []Kline, period int) DMIData {
	if len(klines) <= period {
		return DMIData{PlusDI: 0, MinusDI: 0, ADX: 0}
	}

	n := len(klines)

	// 计算+DM和-DM
	plusDM := make([]float64, n)
	minusDM := make([]float64, n)

	for i := 1; i < n; i++ {
		upMove := klines[i].High - klines[i-1].High
		downMove := klines[i-1].Low - klines[i].Low

		// 根据DMI标准算法，需要比较upMove和downMove
		if upMove > downMove && upMove > 0 {
			plusDM[i] = upMove
		} else {
			plusDM[i] = 0
		}

		if downMove > upMove && downMove > 0 {
			minusDM[i] = downMove
		} else {
			minusDM[i] = 0
		}
	}

	// 计算TR
	tr := make([]float64, n)
	for i := 1; i < n; i++ {
		high := klines[i].High
		low := klines[i].Low
		prevClose := klines[i-1].Close

		tr1 := high - low
		tr2 := math.Abs(high - prevClose)
		tr3 := math.Abs(low - prevClose)

		tr[i] = math.Max(tr1, math.Max(tr2, tr3))
	}

	// 计算平滑的+DM、-DM和TR (Wilder平滑)
	plusDMS := make([]float64, n)
	minusDMS := make([]float64, n)
	trS := make([]float64, n)

	// 初始SMA
	sumPlusDM := 0.0
	sumMinusDM := 0.0
	sumTR := 0.0

	for i := 1; i <= period; i++ {
		sumPlusDM += plusDM[i]
		sumMinusDM += minusDM[i]
		sumTR += tr[i]
	}

	plusDMS[period] = sumPlusDM
	minusDMS[period] = sumMinusDM
	trS[period] = sumTR

	// Wilder平滑: (prior * (n-1) + current) / n
	for i := period + 1; i < n; i++ {
		plusDMS[i] = (plusDMS[i-1]*(float64(period)-1) + plusDM[i]) / float64(period)
		minusDMS[i] = (minusDMS[i-1]*(float64(period)-1) + minusDM[i]) / float64(period)
		trS[i] = (trS[i-1]*(float64(period)-1) + tr[i]) / float64(period)
	}

	// 计算+DI和-DI (取最后一个值)
	var plusDI, minusDI float64
	if trS[n-1] != 0 {
		plusDI = (plusDMS[n-1] / trS[n-1]) * 100
		minusDI = (minusDMS[n-1] / trS[n-1]) * 100
	} else {
		plusDI = 0
		minusDI = 0
	}

	// 计算DX
	dx := make([]float64, n)
	for i := period; i < n; i++ {
		denominator := plusDMS[i] + minusDMS[i]
		if denominator != 0 {
			plusDIValue := (plusDMS[i] / trS[i]) * 100
			minusDIValue := (minusDMS[i] / trS[i]) * 100
			dx[i] = (math.Abs(plusDIValue-minusDIValue) / (plusDIValue + minusDIValue)) * 100
		} else {
			dx[i] = 0
		}
	}

	// 计算ADX
	adxValues := make([]float64, n)

	// 初始SMA
	sumDX := 0.0
	count := 0
	for i := period; i < period*2 && i < n; i++ {
		sumDX += dx[i]
		count++
	}

	if count > 0 {
		adxValues[period*2-1] = sumDX / float64(count)
	}

	// Wilder平滑
	for i := period * 2; i < n; i++ {
		if i-1 >= 0 {
			adxValues[i] = (adxValues[i-1]*(float64(period)-1) + dx[i]) / float64(period)
		} else {
			adxValues[i] = dx[i]
		}
	}

	// 返回最终值
	adx := adxValues[n-1]
	if math.IsNaN(adx) || math.IsInf(adx, 0) {
		adx = 0
	}

	// 确保结果在合理范围内
	if adx > 100 {
		adx = 100
	}

	// 确保结果有效
	if math.IsNaN(plusDI) || math.IsInf(plusDI, 0) {
		plusDI = 0
	}

	if math.IsNaN(minusDI) || math.IsInf(minusDI, 0) {
		minusDI = 0
	}

	return DMIData{PlusDI: plusDI, MinusDI: minusDI, ADX: adx}
}

// calculateKDJ 计算KDJ指标
func calculateKDJ(klines []Kline, period int) KDJData {
	if len(klines) < period {
		return KDJData{K: 0, D: 0, J: 0}
	}

	// 取最近period个周期的数据
	startIndex := len(klines) - period
	recentKlines := klines[startIndex:]

	// 计算周期内的最高价和最低价
	highestHigh := recentKlines[0].High
	lowestLow := recentKlines[0].Low

	for _, kline := range recentKlines {
		if kline.High > highestHigh {
			highestHigh = kline.High
		}
		if kline.Low < lowestLow {
			lowestLow = kline.Low
		}
	}

	// 计算RSV (未成熟随机值)
	closePrice := recentKlines[len(recentKlines)-1].Close
	rsv := 0.0

	if highestHigh-lowestLow != 0 {
		rsv = (closePrice - lowestLow) / (highestHigh - lowestLow) * 100
	} else {
		// 如果最高价等于最低价，RSV设为0
		rsv = 0.0
	}

	// 计算K值和D值
	// 使用递归方式计算前一个KDJ值作为初始值
	var k, d float64
	if len(klines) > period {
		// 使用前一个KDJ值作为初始值
		prevKDJ := calculateKDJ(klines[:len(klines)-1], period)
		k = (2.0/3.0)*prevKDJ.K + (1.0/3.0)*rsv
		d = (2.0/3.0)*prevKDJ.D + (1.0/3.0)*k
	} else {
		// 没有足够的历史数据，使用50作为初始值
		k = (2.0/3.0)*50.0 + (1.0/3.0)*rsv
		d = (2.0/3.0)*50.0 + (1.0/3.0)*k
	}

	// 计算J值: J = 3 * K - 2 * D
	j := 3*k - 2*d

	// 限制范围在0-100之间
	if k > 100 {
		k = 100
	} else if k < 0 {
		k = 0
	}

	if d > 100 {
		d = 100
	} else if d < 0 {
		d = 0
	}

	if j > 100 {
		j = 100
	} else if j < 0 {
		j = 0
	}

	return KDJData{K: k, D: d, J: j}
}

// calculateIntradaySeries 计算日内系列数据
func calculateIntradaySeries(klines []Kline) *IntradayData {
	data := &IntradayData{
		MidPrices:   make([]float64, 0, 10),
		EMA20Values: make([]float64, 0, 10),
		MACDValues:  make([]float64, 0, 10),
		RSI7Values:  make([]float64, 0, 10),
		RSI14Values: make([]float64, 0, 10),
		KDJValues:   make([]KDJData, 0, 10), // KDJ值序列
		DMIValues:   make([]DMIData, 0, 10), // DMI值序列
	}

	// 获取最近10个数据点
	start := len(klines) - 10
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		data.MidPrices = append(data.MidPrices, klines[i].Close)

		// 计算每个点的EMA20
		if i >= 19 {
			ema20 := calculateEMA(klines[:i+1], 20)
			data.EMA20Values = append(data.EMA20Values, ema20)
		}

		// 计算每个点的MACD
		if i >= 25 {
			macd := calculateMACD(klines[:i+1])
			data.MACDValues = append(data.MACDValues, macd)
		}

		// 计算每个点的RSI
		if i >= 7 {
			rsi7 := calculateRSI(klines[:i+1], 7)
			data.RSI7Values = append(data.RSI7Values, rsi7)
		}
		if i >= 14 {
			rsi14 := calculateRSI(klines[:i+1], 14)
			data.RSI14Values = append(data.RSI14Values, rsi14)
		}

		// 计算每个点的KDJ
		if i >= 9 { // KDJ需要至少period个数据点
			kdj := calculateKDJ(klines[:i+1], 9)
			data.KDJValues = append(data.KDJValues, kdj)
		}

		// 计算每个点的DMI
		if i >= 28 { // DMI需要至少2*period个数据点
			dmi := calculateDMI(klines[:i+1], 14)
			data.DMIValues = append(data.DMIValues, dmi)
		}
	}

	return data
}

// calculateLongerTermData 计算长期数据
func calculateLongerTermData(klines []Kline) *LongerTermData {
	data := &LongerTermData{
		KDJValues: make([]KDJData, 0, 10), // KDJ值序列
		DMIValues: make([]DMIData, 0, 10), // DMI值序列
	}

	// 计算EMA
	data.EMA20 = calculateEMA(klines, 20)
	data.EMA50 = calculateEMA(klines, 50)

	// 计算ATR
	data.ATR3 = calculateATR(klines, 3)
	data.ATR14 = calculateATR(klines, 14)

	// 计算成交量
	if len(klines) > 0 {
		data.CurrentVolume = klines[len(klines)-1].Volume
		// 计算平均成交量
		sum := 0.0
		for _, k := range klines {
			sum += k.Volume
		}
		data.AverageVolume = sum / float64(len(klines))
	}

	// 计算MACD和RSI序列
	start := len(klines) - 10
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		if i >= 25 {
			macd := calculateMACD(klines[:i+1])
			data.MACDValues = append(data.MACDValues, macd)
		}
		if i >= 7 {
			rsi7 := calculateRSI(klines[:i+1], 7)
			data.RSI7Values = append(data.RSI7Values, rsi7)
		}
		if i >= 14 {
			rsi14 := calculateRSI(klines[:i+1], 14)
			data.RSI14Values = append(data.RSI14Values, rsi14)
		}
		if i >= 9 { // KDJ需要至少period个数据点
			kdj := calculateKDJ(klines[:i+1], 9)
			data.KDJValues = append(data.KDJValues, kdj)
		}
		if i >= 28 { // DMI需要至少2*period个数据点
			dmi := calculateDMI(klines[:i+1], 14)
			data.DMIValues = append(data.DMIValues, dmi)
		}
	}

	return data
}

// getOpenInterestData 获取OI数据
func getOpenInterestData(symbol string) (*OIData, error) {
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/openInterest?symbol=%s", symbol)

	// 创建带代理的HTTP客户端
	client := NewAPIClient()

	resp, err := client.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		OpenInterest string `json:"openInterest"`
		Symbol       string `json:"symbol"`
		Time         int64  `json:"time"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	oi, _ := strconv.ParseFloat(result.OpenInterest, 64)

	return &OIData{
		Latest:  oi,
		Average: oi * 0.999, // 近似平均值
	}, nil
}

// getFundingRate 获取资金费率（优化：使用 1 小时缓存）
func getFundingRate(symbol string) (float64, error) {
	// 检查缓存（有效期 1 小时）
	// Funding Rate 每 8 小时才更新，1 小时缓存非常合理
	if cached, ok := fundingRateMap.Load(symbol); ok {
		cache := cached.(*FundingRateCache)
		if time.Since(cache.UpdatedAt) < frCacheTTL {
			// 缓存命中，直接返回
			return cache.Rate, nil
		}
	}

	// 缓存过期或不存在，调用 API
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/premiumIndex?symbol=%s", symbol)

	// 创建带代理的HTTP客户端
	client := NewAPIClient()

	resp, err := client.client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var result struct {
		Symbol          string `json:"symbol"`
		MarkPrice       string `json:"markPrice"`
		IndexPrice      string `json:"indexPrice"`
		LastFundingRate string `json:"lastFundingRate"`
		NextFundingTime int64  `json:"nextFundingTime"`
		InterestRate    string `json:"interestRate"`
		Time            int64  `json:"time"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, err
	}

	rate, _ := strconv.ParseFloat(result.LastFundingRate, 64)

	// 更新缓存
	fundingRateMap.Store(symbol, &FundingRateCache{
		Rate:      rate,
		UpdatedAt: time.Now(),
	})

	return rate, nil
}

// Format 格式化输出市场数据
func Format(data *Data) string {
	var sb strings.Builder

	// 使用动态精度格式化价格
	priceStr := formatPriceWithDynamicPrecision(data.CurrentPrice)
	sb.WriteString(fmt.Sprintf("current_price = %s, current_ema20 = %.3f, current_macd = %.3f, current_rsi (7 period) = %.3f\n\n",
		priceStr, data.CurrentEMA20, data.CurrentMACD, data.CurrentRSI7))

	// 添加KDJ和DMI指标显示
	sb.WriteString(fmt.Sprintf("KDJ indicator (9 period): K = %.3f, D = %.3f, J = %.3f\n",
		data.CurrentKDJ.K, data.CurrentKDJ.D, data.CurrentKDJ.J))
	sb.WriteString(fmt.Sprintf("DMI indicator (14 period): +DI = %.3f, -DI = %.3f, ADX = %.3f\n\n",
		data.CurrentDMI.PlusDI, data.CurrentDMI.MinusDI, data.CurrentDMI.ADX))

	// 添加DOM指标显示
	sb.WriteString(fmt.Sprintf("DOM indicator: Bid Depth = %.2f, Ask Depth = %.2f, Depth Ratio = %.3f\n\n",
		data.DOMData.BidDepth, data.DOMData.AskDepth, data.DOMData.DepthRatio))

	// 添加4小时时间框架的指标显示
	sb.WriteString(fmt.Sprintf("4H Timeframe Indicators:\n"))
	sb.WriteString(fmt.Sprintf("  EMA20 = %.3f, MACD = %.3f\n", data.HourlyEMA20, data.HourlyMACD))
	sb.WriteString(fmt.Sprintf("  RSI(7) = %.3f, RSI(14) = %.3f\n", data.HourlyRSI7, data.HourlyRSI14))
	sb.WriteString(fmt.Sprintf("  KDJ: K = %.3f, D = %.3f, J = %.3f\n",
		data.HourlyKDJ.K, data.HourlyKDJ.D, data.HourlyKDJ.J))
	sb.WriteString(fmt.Sprintf("  DMI: +DI = %.3f, -DI = %.3f, ADX = %.3f\n\n",
		data.HourlyDMI.PlusDI, data.HourlyDMI.MinusDI, data.HourlyDMI.ADX))

	sb.WriteString(fmt.Sprintf("In addition, here is the latest %s open interest and funding rate for perps:\n\n",
		data.Symbol))

	if data.OpenInterest != nil {
		// 使用动态精度格式化 OI 数据
		oiLatestStr := formatPriceWithDynamicPrecision(data.OpenInterest.Latest)
		oiAverageStr := formatPriceWithDynamicPrecision(data.OpenInterest.Average)
		sb.WriteString(fmt.Sprintf("Open Interest: Latest: %s Average: %s\n\n",
			oiLatestStr, oiAverageStr))
	}

	sb.WriteString(fmt.Sprintf("Funding Rate: %.2e\n\n", data.FundingRate))

	if data.IntradaySeries != nil {
		sb.WriteString("Intraday series (15‑minute intervals, oldest → latest):\n\n")

		if len(data.IntradaySeries.MidPrices) > 0 {
			sb.WriteString(fmt.Sprintf("Mid prices: %s\n\n", formatFloatSlice(data.IntradaySeries.MidPrices)))
		}

		if len(data.IntradaySeries.EMA20Values) > 0 {
			sb.WriteString(fmt.Sprintf("EMA indicators (20‑period): %s\n\n", formatFloatSlice(data.IntradaySeries.EMA20Values)))
		}

		if len(data.IntradaySeries.MACDValues) > 0 {
			sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.IntradaySeries.MACDValues)))
		}

		if len(data.IntradaySeries.RSI7Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (7‑Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI7Values)))
		}

		if len(data.IntradaySeries.RSI14Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (14‑Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI14Values)))
		}

		// 添加KDJ和DMI序列显示
		if len(data.IntradaySeries.KDJValues) > 0 {
			kValues := make([]float64, len(data.IntradaySeries.KDJValues))
			dValues := make([]float64, len(data.IntradaySeries.KDJValues))
			jValues := make([]float64, len(data.IntradaySeries.KDJValues))

			for i, kdj := range data.IntradaySeries.KDJValues {
				kValues[i] = kdj.K
				dValues[i] = kdj.D
				jValues[i] = kdj.J
			}

			sb.WriteString(fmt.Sprintf("KDJ K values (9‑Period): %s\n\n", formatFloatSlice(kValues)))
			sb.WriteString(fmt.Sprintf("KDJ D values (9‑Period): %s\n\n", formatFloatSlice(dValues)))
			sb.WriteString(fmt.Sprintf("KDJ J values (9‑Period): %s\n\n", formatFloatSlice(jValues)))
		}

		if len(data.IntradaySeries.DMIValues) > 0 {
			plusDIValues := make([]float64, len(data.IntradaySeries.DMIValues))
			minusDIValues := make([]float64, len(data.IntradaySeries.DMIValues))
			adxValues := make([]float64, len(data.IntradaySeries.DMIValues))

			for i, dmi := range data.IntradaySeries.DMIValues {
				plusDIValues[i] = dmi.PlusDI
				minusDIValues[i] = dmi.MinusDI
				adxValues[i] = dmi.ADX
			}

			sb.WriteString(fmt.Sprintf("+DI values (14‑Period): %s\n\n", formatFloatSlice(plusDIValues)))
			sb.WriteString(fmt.Sprintf("-DI values (14‑Period): %s\n\n", formatFloatSlice(minusDIValues)))
			sb.WriteString(fmt.Sprintf("ADX values (14‑Period): %s\n\n", formatFloatSlice(adxValues)))
		}
	}

	if data.LongerTermContext != nil {
		sb.WriteString("Longer‑term context (4‑hour timeframe):\n\n")

		sb.WriteString(fmt.Sprintf("20‑Period EMA: %.3f vs. 50‑Period EMA: %.3f\n\n",
			data.LongerTermContext.EMA20, data.LongerTermContext.EMA50))

		sb.WriteString(fmt.Sprintf("3‑Period ATR: %.3f vs. 14‑Period ATR: %.3f\n\n",
			data.LongerTermContext.ATR3, data.LongerTermContext.ATR14))

		sb.WriteString(fmt.Sprintf("Current Volume: %.3f vs. Average Volume: %.3f\n\n",
			data.LongerTermContext.CurrentVolume, data.LongerTermContext.AverageVolume))

		if len(data.LongerTermContext.MACDValues) > 0 {
			sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.LongerTermContext.MACDValues)))
		}

		if len(data.LongerTermContext.RSI7Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (7‑Period): %s\n\n", formatFloatSlice(data.LongerTermContext.RSI7Values)))
		}

		if len(data.LongerTermContext.RSI14Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (14‑Period): %s\n\n", formatFloatSlice(data.LongerTermContext.RSI14Values)))
		}

		// 添加KDJ和DMI序列显示
		if len(data.LongerTermContext.KDJValues) > 0 {
			kValues := make([]float64, len(data.LongerTermContext.KDJValues))
			dValues := make([]float64, len(data.LongerTermContext.KDJValues))
			jValues := make([]float64, len(data.LongerTermContext.KDJValues))

			for i, kdj := range data.LongerTermContext.KDJValues {
				kValues[i] = kdj.K
				dValues[i] = kdj.D
				jValues[i] = kdj.J
			}

			sb.WriteString(fmt.Sprintf("KDJ K values (9‑Period): %s\n\n", formatFloatSlice(kValues)))
			sb.WriteString(fmt.Sprintf("KDJ D values (9‑Period): %s\n\n", formatFloatSlice(dValues)))
			sb.WriteString(fmt.Sprintf("KDJ J values (9‑Period): %s\n\n", formatFloatSlice(jValues)))
		}

		if len(data.LongerTermContext.DMIValues) > 0 {
			plusDIValues := make([]float64, len(data.LongerTermContext.DMIValues))
			minusDIValues := make([]float64, len(data.LongerTermContext.DMIValues))
			adxValues := make([]float64, len(data.LongerTermContext.DMIValues))

			for i, dmi := range data.LongerTermContext.DMIValues {
				plusDIValues[i] = dmi.PlusDI
				minusDIValues[i] = dmi.MinusDI
				adxValues[i] = dmi.ADX
			}

			sb.WriteString(fmt.Sprintf("+DI values (14‑Period): %s\n\n", formatFloatSlice(plusDIValues)))
			sb.WriteString(fmt.Sprintf("-DI values (14‑Period): %s\n\n", formatFloatSlice(minusDIValues)))
			sb.WriteString(fmt.Sprintf("ADX values (14‑Period): %s\n\n", formatFloatSlice(adxValues)))
		}
	}

	return sb.String()
}

// formatPriceWithDynamicPrecision 根据价格区间动态选择精度
// 这样可以完美支持从超低价 meme coin (< 0.0001) 到 BTC/ETH 的所有币种
func formatPriceWithDynamicPrecision(price float64) string {
	switch {
	case price < 0.0001:
		// 超低价 meme coin: 1000SATS, 1000WHY, DOGS
		// 0.00002070 → "0.00002070" (8位小数)
		return fmt.Sprintf("%.8f", price)
	case price < 0.001:
		// 低价 meme coin: NEIRO, HMSTR, HOT, NOT
		// 0.00015060 → "0.000151" (6位小数)
		return fmt.Sprintf("%.6f", price)
	case price < 0.01:
		// 中低价币: PEPE, SHIB, MEME
		// 0.00556800 → "0.005568" (6位小数)
		return fmt.Sprintf("%.6f", price)
	case price < 1.0:
		// 低价币: ASTER, DOGE, ADA, TRX
		// 0.9954 → "0.9954" (4位小数)
		return fmt.Sprintf("%.4f", price)
	case price < 100:
		// 中价币: SOL, AVAX, LINK, MATIC
		// 23.4567 → "23.4567" (4位小数)
		return fmt.Sprintf("%.4f", price)
	default:
		// 高价币: BTC, ETH (节省 Token)
		// 45678.9123 → "45678.91" (2位小数)
		return fmt.Sprintf("%.2f", price)
	}
}

// formatFloatSlice 格式化float64切片为字符串（使用动态精度）
func formatFloatSlice(values []float64) string {
	strValues := make([]string, len(values))
	for i, v := range values {
		strValues[i] = formatPriceWithDynamicPrecision(v)
	}
	return "[" + strings.Join(strValues, ", ") + "]"
}

// Normalize 标准化symbol,确保是USDT交易对
func Normalize(symbol string) string {
	symbol = strings.ToUpper(symbol)
	if strings.HasSuffix(symbol, "USDT") {
		return symbol
	}
	return symbol + "USDT"
}

// parseFloat 解析float值
func parseFloat(v interface{}) (float64, error) {
	switch val := v.(type) {
	case string:
		return strconv.ParseFloat(val, 64)
	case float64:
		return val, nil
	case int:
		return float64(val), nil
	case int64:
		return float64(val), nil
	default:
		return 0, fmt.Errorf("unsupported type: %T", v)
	}
}
