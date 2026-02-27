package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
	"stonks/internal/models"
	"time"
)

// monthlyHandler serves the monthly performance view
func (s *Server) monthlyHandler(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters for month filtering (YYYY-MM format)
	fromMonth := r.URL.Query().Get("from")
	toMonth := r.URL.Query().Get("to")
	
	// Set default date range to 1 year (12 months: current month back 11 months)
	if fromMonth == "" || toMonth == "" {
		now := time.Now()
		toMonth = fmt.Sprintf("%04d-%02d", now.Year(), now.Month())
		elevenMonthsAgo := now.AddDate(0, -11, 0)
		fromMonth = fmt.Sprintf("%04d-%02d", elevenMonthsAgo.Year(), elevenMonthsAgo.Month())
	}
	
	symbols, err := s.symbolService.GetDistinctSymbols()
	if err != nil {
		symbols = []string{}
	}

	// Get all options for calculations
	options, err := s.optionService.GetAll()
	if err != nil {
		options = []*models.Option{}
	}

	// Create options index for advanced filtering
	optionsIndex, err := s.optionService.Index()
	if err != nil {
		optionsIndex = make(map[string]interface{})
	}

	// Get all dividends for calculations
	dividends, err := s.dividendService.GetAll()
	if err != nil {
		dividends = []*models.Dividend{}
	}

	// Get all long positions for capital gains calculations
	longPositions, err := s.longPositionService.GetAll()
	if err != nil {
		longPositions = []*models.LongPosition{}
	}

	// Build monthly data with month filtering
	data := s.buildMonthlyData(symbols, options, dividends, longPositions, optionsIndex, fromMonth, toMonth)

	s.renderTemplate(w, "monthly.html", data)
}

// calculateMaxCollateral finds the peak simultaneous collateral during a given month.
// Collateral = Put collateral (Strike × Contracts × 100) + Long position cost basis (BuyPrice × Shares).
func calculateMaxCollateral(ym string, options []*models.Option, positions []*models.LongPosition) float64 {
	var year, month int
	fmt.Sscanf(ym, "%04d-%02d", &year, &month)
	startDate := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	endDate := startDate.AddDate(0, 1, 0) // exclusive: first of next month

	type Event struct {
		date  time.Time
		delta float64
	}
	var events []Event
	var initialCollateral float64

	// Put options: collateral = Strike × Contracts × 100
	for _, opt := range options {
		if opt.Type != "Put" {
			continue
		}
		putStillActive := opt.Closed == nil
		var closeDate time.Time
		if !putStillActive {
			closeDate = *opt.Closed
		}

		// Skip if doesn't overlap with this month
		if !opt.Opened.Before(endDate) {
			continue
		}
		if !putStillActive && closeDate.Before(startDate) {
			continue
		}

		collateral := opt.Strike * float64(opt.Contracts) * 100
		if opt.Opened.Before(startDate) {
			initialCollateral += collateral
			// Closes during the month
			if !putStillActive && closeDate.Before(endDate) {
				events = append(events, Event{closeDate, -collateral})
			}
		} else {
			// Opens during the month
			events = append(events, Event{opt.Opened, +collateral})
			if !putStillActive && closeDate.Before(endDate) {
				events = append(events, Event{closeDate, -collateral})
			}
		}
	}

	// Long positions: collateral = BuyPrice × Shares
	for _, pos := range positions {
		posStillActive := pos.Closed == nil
		var closeDate time.Time
		if !posStillActive {
			closeDate = *pos.Closed
		}

		if !pos.Opened.Before(endDate) {
			continue
		}
		if !posStillActive && closeDate.Before(startDate) {
			continue
		}

		costBasis := pos.BuyPrice * float64(pos.Shares)
		if pos.Opened.Before(startDate) {
			initialCollateral += costBasis
			if !posStillActive && closeDate.Before(endDate) {
				events = append(events, Event{closeDate, -costBasis})
			}
		} else {
			events = append(events, Event{pos.Opened, +costBasis})
			if !posStillActive && closeDate.Before(endDate) {
				events = append(events, Event{closeDate, -costBasis})
			}
		}
	}

	sort.Slice(events, func(i, j int) bool { return events[i].date.Before(events[j].date) })
	current := initialCollateral
	maxCollateral := initialCollateral
	for _, e := range events {
		current += e.delta
		if current > maxCollateral {
			maxCollateral = current
		}
	}
	return maxCollateral
}

// buildMonthlyData creates comprehensive monthly financial data based on transaction dates
func (s *Server) buildMonthlyData(symbols []string, options []*models.Option, dividends []*models.Dividend, longPositions []*models.LongPosition, optionsIndex map[string]interface{}, fromMonth, toMonth string) MonthlyData {
	// Initialize data structures - use YYYY-MM keys instead of month indexes
	putsByYearMonth := make(map[string]float64)      // yyyy-mm -> total
	callsByYearMonth := make(map[string]float64)     // yyyy-mm -> total
	putsByTicker := make(map[string]float64)         // ticker -> total
	callsByTicker := make(map[string]float64)        // ticker -> total
	capGainsByYearMonth := make(map[string]float64)  // yyyy-mm -> total
	dividendsByYearMonth := make(map[string]float64) // yyyy-mm -> total
	capGainsByTicker := make(map[string]float64)     // ticker -> total
	dividendsByTicker := make(map[string]float64)    // ticker -> total

	// Ticker -> YearMonth (yyyy-mm) -> Amount for table
	tickerMonthData := make(map[string]map[string]float64)
	
	// Track all unique year-months
	yearMonthSet := make(map[string]bool)
	

	// Process all options (both open and closed)
	for _, option := range options {
		// Calculate profit/loss for all options (premium realized at open)
		totalPremium := option.CalculateTotalProfit()
		
		// Get yyyy-mm for all aggregations
		yearMonth := fmt.Sprintf("%04d-%02d", option.Opened.Year(), option.Opened.Month())
		
		// Apply date range filter
		if fromMonth != "" && yearMonth < fromMonth {
			continue
		}
		if toMonth != "" && yearMonth > toMonth {
			continue
		}
		
		yearMonthSet[yearMonth] = true

		// Aggregate by year-month and type
		if option.Type == "Put" {
			putsByYearMonth[yearMonth] += totalPremium
			putsByTicker[option.Symbol] += totalPremium
		} else if option.Type == "Call" {
			callsByYearMonth[yearMonth] += totalPremium
			callsByTicker[option.Symbol] += totalPremium
		}

		// Aggregate for table data (both puts and calls combined)
		if data, exists := tickerMonthData[option.Symbol]; exists {
			data[yearMonth] += totalPremium
		} else {
			newData := make(map[string]float64)
			newData[yearMonth] = totalPremium
			tickerMonthData[option.Symbol] = newData
		}

	}

	// Process all dividends (based on received date)
	for _, dividend := range dividends {
		amount := dividend.Amount
		
		// Get yyyy-mm
		yearMonth := fmt.Sprintf("%04d-%02d", dividend.Received.Year(), dividend.Received.Month())
		
		// Apply date range filter
		if fromMonth != "" && yearMonth < fromMonth {
			continue
		}
		if toMonth != "" && yearMonth > toMonth {
			continue
		}
		
		yearMonthSet[yearMonth] = true

		// Aggregate by year-month and ticker
		dividendsByYearMonth[yearMonth] += amount
		dividendsByTicker[dividend.Symbol] += amount

		// Aggregate for table data
		if data, exists := tickerMonthData[dividend.Symbol]; exists {
			data[yearMonth] += amount
		} else {
			newData := make(map[string]float64)
			newData[yearMonth] = amount
			tickerMonthData[dividend.Symbol] = newData
		}
	}

	// Process capital gains only from closed long positions (realized gains only)
	for _, position := range longPositions {
		if position.Closed != nil {
			// Calculate profit/loss for closed position
			profit := (position.GetExitPriceValue() - position.BuyPrice) * float64(position.Shares)
			
			// Get yyyy-mm
			yearMonth := fmt.Sprintf("%04d-%02d", position.Closed.Year(), position.Closed.Month())
			
			// Apply date range filter
			if fromMonth != "" && yearMonth < fromMonth {
				continue
			}
			if toMonth != "" && yearMonth > toMonth {
				continue
			}
			
			yearMonthSet[yearMonth] = true

			// Aggregate by year-month and ticker
			capGainsByYearMonth[yearMonth] += profit
			capGainsByTicker[position.Symbol] += profit

			// Aggregate for table data
			if data, exists := tickerMonthData[position.Symbol]; exists {
				data[yearMonth] += profit
			} else {
				newData := make(map[string]float64)
				newData[yearMonth] = profit
				tickerMonthData[position.Symbol] = newData
			}
		}
	}

	// Convert year-month set to sorted slice
	yearMonths := make([]string, 0, len(yearMonthSet))
	for ym := range yearMonthSet {
		yearMonths = append(yearMonths, ym)
	}
	sort.Strings(yearMonths) // Sort ascending

	// Build chart data for Puts by month using YYYY-MM
	putsMonthChart := make([]MonthlyChartData, len(yearMonths))
	for i, ym := range yearMonths {
		putsMonthChart[i] = MonthlyChartData{
			Month:  ym,
			Amount: putsByYearMonth[ym],
		}
	}

	// Build chart data for Calls by month using YYYY-MM
	callsMonthChart := make([]MonthlyChartData, len(yearMonths))
	for i, ym := range yearMonths {
		callsMonthChart[i] = MonthlyChartData{
			Month:  ym,
			Amount: callsByYearMonth[ym],
		}
	}

	// Build ticker chart data for Puts
	putsTickerChart := []TickerChartData{}
	for ticker, amount := range putsByTicker {
		if amount != 0 {
			putsTickerChart = append(putsTickerChart, TickerChartData{
				Ticker: ticker,
				Amount: amount,
			})
		}
	}

	// Build ticker chart data for Calls
	callsTickerChart := []TickerChartData{}
	for ticker, amount := range callsByTicker {
		if amount != 0 {
			callsTickerChart = append(callsTickerChart, TickerChartData{
				Ticker: ticker,
				Amount: amount,
			})
		}
	}

	// Build chart data for Capital Gains by month using YYYY-MM
	capGainsMonthChart := make([]MonthlyChartData, len(yearMonths))
	for i, ym := range yearMonths {
		capGainsMonthChart[i] = MonthlyChartData{
			Month:  ym,
			Amount: capGainsByYearMonth[ym],
		}
	}

	// Build ticker chart data for Capital Gains
	capGainsTickerChart := []TickerChartData{}
	for ticker, amount := range capGainsByTicker {
		if amount != 0 {
			capGainsTickerChart = append(capGainsTickerChart, TickerChartData{
				Ticker: ticker,
				Amount: amount,
			})
		}
	}

	// Build chart data for Dividends by month using YYYY-MM
	dividendsMonthChart := make([]MonthlyChartData, len(yearMonths))
	for i, ym := range yearMonths {
		dividendsMonthChart[i] = MonthlyChartData{
			Month:  ym,
			Amount: dividendsByYearMonth[ym],
		}
	}

	// Build ticker chart data for Dividends
	dividendsTickerChart := []TickerChartData{}
	for ticker, amount := range dividendsByTicker {
		if amount != 0 {
			dividendsTickerChart = append(dividendsTickerChart, TickerChartData{
				Ticker: ticker,
				Amount: amount,
			})
		}
	}

	// Create formatted labels ("2025 Jan", etc.)
	monthLabels := []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	formattedLabels := make([]string, len(yearMonths))
	for i, ym := range yearMonths {
		// Parse yyyy-mm
		var year, month int
		fmt.Sscanf(ym, "%04d-%02d", &year, &month)
		formattedLabels[i] = fmt.Sprintf("%d %s", year, monthLabels[month-1])
	}
	
	// Build table data
	tableData := []MonthlyTableRow{}
	for ticker, monthValues := range tickerMonthData {
		// Calculate total for this ticker
		total := 0.0
		for _, amount := range monthValues {
			total += amount
		}

		if total != 0 {
			tableData = append(tableData, MonthlyTableRow{
				Ticker:      ticker,
				Total:       total,
				MonthValues: monthValues,
			})
		}
	}

	// Calculate totals by year-month for the table footer
	totalsByYearMonth := make(map[string]float64)
	grandTotal := 0.0
	
	// Aggregate totals from all data sources
	for ticker, monthValues := range tickerMonthData {
		_ = ticker // Unused, just iterating
		for ym, amount := range monthValues {
			totalsByYearMonth[ym] += amount
			grandTotal += amount
		}
	}
	
	// Build totals by month using YYYY-MM
	totalsByMonth := make([]MonthlyTotal, len(yearMonths))
	for i, ym := range yearMonths {
		total := putsByYearMonth[ym] + callsByYearMonth[ym] + capGainsByYearMonth[ym] + dividendsByYearMonth[ym]
		totalsByMonth[i] = MonthlyTotal{
			Month:  ym,
			Amount: total,
		}
	}

	// Build collateral and APR chart data
	collateralByMonth := make([]MonthlyChartData, len(yearMonths))
	aprByMonth := make([]MonthlyChartData, len(yearMonths))
	for i, ym := range yearMonths {
		maxCollateral := calculateMaxCollateral(ym, options, longPositions)
		optionsPremium := putsByYearMonth[ym] + callsByYearMonth[ym]
		var apr float64
		if maxCollateral > 0 {
			apr = (optionsPremium / maxCollateral) * 12 * 100
		}
		collateralByMonth[i] = MonthlyChartData{Month: ym, Amount: maxCollateral}
		aprByMonth[i] = MonthlyChartData{Month: ym, Amount: apr}
	}

	// MonthlyPremiumsBySymbol is deprecated - frontend uses OptionsIndex instead
	monthlyPremiumsBySymbol := []MonthlyPremiumsBySymbol{}
	
	// Convert options index to JSON for template
	indexJSON, err := json.Marshal(optionsIndex)
	if err != nil {
		log.Printf("[MONTHLY PAGE] ERROR: Failed to marshal options index to JSON: %v", err)
		indexJSON = []byte("{}")
	}

	return MonthlyData{
		Symbols:    symbols,
		AllSymbols: symbols, // For navigation compatibility
		PutsData: MonthlyOptionData{
			ByMonth:  putsMonthChart,
			ByTicker: putsTickerChart,
		},
		CallsData: MonthlyOptionData{
			ByMonth:  callsMonthChart,
			ByTicker: callsTickerChart,
		},
		CapGainsData: MonthlyFinancialData{
			ByMonth:  capGainsMonthChart,
			ByTicker: capGainsTickerChart,
		},
		DividendsData: MonthlyFinancialData{
			ByMonth:  dividendsMonthChart,
			ByTicker: dividendsTickerChart,
		},
		TableData:               tableData,
		TableYearMonths:         yearMonths,
		TableMonthLabels:        formattedLabels,
		TableTotalsByMonth:      totalsByYearMonth,
		TotalsByMonth:           totalsByMonth,
		CollateralByMonth:       collateralByMonth,
		APRByMonth:              aprByMonth,
		MonthlyPremiumsBySymbol: monthlyPremiumsBySymbol,
		OptionsIndex:            optionsIndex,
		OptionsIndexJSON:        template.JS(string(indexJSON)),
		GrandTotal:              grandTotal,
		CurrentDB:               s.getCurrentDatabaseName(),
		ActivePage:              "monthly",
		SelectedFromDate:        fromMonth,
		SelectedToDate:          toMonth,
	}
}