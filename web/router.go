package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/azer/logger"
	"github.com/gorilla/mux"
	"github.com/oschwald/maxminddb-golang"

	webErrors "github.com/GitbookIO/micro-analytics/web/errors"
	. "github.com/GitbookIO/micro-analytics/web/structures"

	"github.com/GitbookIO/micro-analytics/database"
	driverErrors "github.com/GitbookIO/micro-analytics/database/errors"
	"github.com/GitbookIO/micro-analytics/database/sqlite"

	"github.com/GitbookIO/micro-analytics/utils"
	"github.com/GitbookIO/micro-analytics/utils/geoip"
)

type RouterOpts struct {
	DriverOpts     database.DriverOpts
	Geolite2Reader *maxminddb.Reader
	Version        string
}

func NewRouter(opts RouterOpts) (http.Handler, error) {
	// Create the app router
	r := mux.NewRouter()
	var log = logger.New("[Router]")

	geolite2 := opts.Geolite2Reader

	// Initiate DB driver
	driver, err := sqlite.NewShardedDriver(opts.DriverOpts)
	if err != nil {
		return nil, err
	}

	/////
	// Welcome
	/////
	r.Path("/").
		Methods("GET").
		HandlerFunc(func(w http.ResponseWriter, req *http.Request) {

		msg := map[string]string{
			"message": "Welcome to analytics !",
			"version": opts.Version,
		}
		render(w, msg, nil)
	})

	/////
	// Query a DB over time
	/////
	r.Path("/{dbName}/time").
		Methods("GET").
		HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Get params from URL
		vars := mux.Vars(req)
		dbName := vars["dbName"]

		// Parse request query
		if err := req.ParseForm(); err != nil {
			renderError(w, err)
			return
		}

		// Get timeRange if provided
		startTime := req.Form.Get("start")
		endTime := req.Form.Get("end")
		intervalStr := req.Form.Get("interval")

		// Convert startTime and endTime to a TimeRange
		timeRange, err := newTimeRange(startTime, endTime)
		if err != nil {
			renderError(w, &webErrors.InvalidTimeFormat)
			return
		}

		// Cast interval to an integer
		// Defaults to 1 day
		interval := 24 * 60 * 60
		if len(intervalStr) > 0 {
			interval, err = strconv.Atoi(intervalStr)
			if err != nil {
				renderError(w, &webErrors.InvalidInterval)
				return
			}
		}

		unique := false
		if strings.Compare(req.Form.Get("unique"), "true") == 0 {
			unique = true
		}

		// Construct Params object
		params := database.Params{
			DBName:    dbName,
			Interval:  interval,
			TimeRange: timeRange,
			Unique:    unique,
			URL:       req.URL,
		}

		analytics, err := driver.Series(params)
		if err != nil {
			if driverErr, ok := err.(*driverErrors.DriverError); ok {
				switch driverErr.Code {
				case 1:
					renderError(w, &webErrors.InternalError)
				case 2:
					renderError(w, &webErrors.InvalidDatabaseName)
				case 3:
					renderError(w, &webErrors.InvalidTimeFormat)
				default:
					renderError(w, err)
				}
				return
			}
			renderError(w, err)
			return
		}

		// Return query result
		render(w, analytics, nil)
	})

	/////
	// Query a DB by property
	/////
	r.Path("/{dbName}/{property}").
		Methods("GET").
		HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Map allowed requests w/ columns names in DB schema
		allowedProperties := map[string]string{
			"countries": "countryCode",
			"platforms": "platform",
			"domains":   "refererDomain",
			"events":    "event",
		}
		// Get params from URL
		vars := mux.Vars(req)
		dbName := vars["dbName"]
		property := vars["property"]

		// Check that property is allowed to be queried
		property, ok := allowedProperties[property]
		if !ok {
			renderError(w, &webErrors.InvalidProperty)
			return
		}

		// Parse request query
		if err := req.ParseForm(); err != nil {
			renderError(w, err)
			return
		}

		// Get timeRange if provided
		startTime := req.Form.Get("start")
		endTime := req.Form.Get("end")

		timeRange, err := newTimeRange(startTime, endTime)
		if err != nil {
			renderError(w, &webErrors.InvalidTimeFormat)
			return
		}

		unique := false
		if strings.Compare(req.Form.Get("unique"), "true") == 0 {
			unique = true
		}

		// Construct Params object
		params := database.Params{
			DBName:    dbName,
			Property:  property,
			TimeRange: timeRange,
			Unique:    unique,
			URL:       req.URL,
		}

		analytics, err := driver.GroupBy(params)
		if err != nil {
			if driverErr, ok := err.(*driverErrors.DriverError); ok {
				switch driverErr.Code {
				case 1:
					renderError(w, &webErrors.InternalError)
				case 2:
					renderError(w, &webErrors.InvalidDatabaseName)
				default:
					renderError(w, err)
				}
				return
			}
			renderError(w, err)
			return
		}

		// Return query result
		render(w, analytics, nil)
	})

	/////
	// Full query a DB
	/////
	r.Path("/{dbName}").
		Methods("GET").
		HandlerFunc(func(w http.ResponseWriter, req *http.Request) {

		// Get dbName from URL
		vars := mux.Vars(req)
		dbName := vars["dbName"]

		// Parse request query
		if err := req.ParseForm(); err != nil {
			renderError(w, err)
			return
		}

		// Get timeRange if provided
		startTime := req.Form.Get("start")
		endTime := req.Form.Get("end")

		timeRange, err := newTimeRange(startTime, endTime)
		if err != nil {
			renderError(w, &webErrors.InvalidTimeFormat)
			return
		}

		// Construct Params object
		params := database.Params{
			DBName:    dbName,
			TimeRange: timeRange,
			URL:       req.URL,
		}

		analytics, err := driver.Query(params)
		if err != nil {
			if driverErr, ok := err.(*driverErrors.DriverError); ok {
				switch driverErr.Code {
				case 1:
					renderError(w, &webErrors.InternalError)
				case 2:
					renderError(w, &webErrors.InvalidDatabaseName)
				default:
					renderError(w, err)
				}
				return
			}
			renderError(w, err)
			return
		}

		render(w, analytics, nil)
	})

	/////
	// Push analytics to a DB
	/////
	r.Path("/{dbName}").
		Methods("POST").
		HandlerFunc(func(w http.ResponseWriter, req *http.Request) {

		// Get dbName from URL
		vars := mux.Vars(req)
		dbName := vars["dbName"]

		// Parse JSON POST data
		postData := PostData{}
		jsonDecoder := json.NewDecoder(req.Body)
		err := jsonDecoder.Decode(&postData)

		// Invalid JSON
		if err != nil {
			renderError(w, &webErrors.InvalidJSON)
			return
		}

		// Create Analytic to inject in DB
		analytic := database.Analytic{
			Time:  time.Now(),
			Event: postData.Event,
			Path:  postData.Path,
			Ip:    postData.Ip,
		}

		// Set time from POST data if passed
		if len(postData.Time) > 0 {
			analytic.Time, err = time.Parse(time.RFC3339, postData.Time)
		}

		// Get referer from headers
		refererHeader := postData.Headers["referer"]
		if referrerURL, err := url.ParseRequestURI(refererHeader); err == nil {
			analytic.RefererDomain = referrerURL.Host
		}

		// Get platform from headers
		analytic.Platform = utils.Platform(postData.Headers["user-agent"])

		// Get countryCode from GeoIp
		analytic.CountryCode, err = geoip.GeoIpLookup(geolite2, postData.Ip)

		// Construct Params object
		params := database.Params{
			DBName: dbName,
		}

		err = driver.Insert(params, analytic)
		if err != nil {
			if _, ok := err.(*driverErrors.DriverError); ok {
				renderError(w, &webErrors.InsertFailed)
				return
			}
			renderError(w, err)
			return
		}

		log.Info("Successfully inserted analytic: %#v", analytic)

		render(w, nil, nil)
	})

	/////
	// Push analytics as-is to a DB
	/////
	r.Path("/{dbName}/special").
		Methods("POST").
		HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Get dbName from URL
		vars := mux.Vars(req)
		dbName := vars["dbName"]

		// Parse JSON POST data
		postData := PostAnalytic{}
		jsonDecoder := json.NewDecoder(req.Body)
		err := jsonDecoder.Decode(&postData)

		// Invalid JSON
		if err != nil {
			renderError(w, &webErrors.InvalidJSON)
			return
		}

		// Create Analytic to inject in DB
		analytic := database.Analytic{
			Time:          time.Unix(int64(postData.Time), 0),
			Event:         postData.Event,
			Path:          postData.Path,
			Ip:            postData.Ip,
			Platform:      postData.Platform,
			RefererDomain: postData.RefererDomain,
			CountryCode:   postData.CountryCode,
		}

		// Construct Params object
		params := database.Params{
			DBName: dbName,
		}

		err = driver.Insert(params, analytic)
		if err != nil {
			if _, ok := err.(*driverErrors.DriverError); ok {
				renderError(w, &webErrors.InsertFailed)
				return
			}
			renderError(w, err)
			return
		}

		log.Info("Successfully inserted analytic: %#v", analytic)

		render(w, nil, nil)
	})

	/////
	// Push a list of analytics as-is to a DB
	/////
	r.Path("/{dbName}/bulk").
		Methods("POST").
		HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Get dbName from URL
		vars := mux.Vars(req)
		dbName := vars["dbName"]

		// Parse JSON POST data
		postList := PostAnalytics{}
		jsonDecoder := json.NewDecoder(req.Body)
		err := jsonDecoder.Decode(&postList)

		// Invalid JSON
		if err != nil {
			renderError(w, &webErrors.InvalidJSON)
			return
		}

		for _, postData := range postList.List {
			// Create Analytic to inject in DB
			analytic := database.Analytic{
				Time:          time.Unix(int64(postData.Time), 0),
				Event:         postData.Event,
				Path:          postData.Path,
				Ip:            postData.Ip,
				Platform:      postData.Platform,
				RefererDomain: postData.RefererDomain,
				CountryCode:   postData.CountryCode,
			}

			// Get countryCode from GeoIp
			analytic.CountryCode, err = geoip.GeoIpLookup(geolite2, postData.Ip)
			if err != nil {
				log.Error("Error [%v] looking for countryCode for IP %s", postData.Ip)
			}

			// Construct Params object
			params := database.Params{
				DBName: dbName,
			}

			err = driver.Insert(params, analytic)
			if err != nil {
				if _, ok := err.(*driverErrors.DriverError); ok {
					renderError(w, &webErrors.InsertFailed)
					return
				}
				renderError(w, err)
				return
			}

			log.Info("Successfully inserted analytic: %#v", analytic)
		}

		render(w, nil, nil)
	})

	/////
	// Delete a DB
	/////
	r.Path("/{dbName}").
		Methods("DELETE").
		HandlerFunc(func(w http.ResponseWriter, req *http.Request) {

		// Get dbName from URL
		vars := mux.Vars(req)
		dbName := vars["dbName"]

		// Construct Params object
		params := database.Params{
			DBName: dbName,
		}

		err := driver.Delete(params)
		if err != nil {
			if driverErr, ok := err.(*driverErrors.DriverError); ok {
				switch driverErr.Code {
				case 1:
					renderError(w, &webErrors.InternalError)
				case 2:
					renderError(w, &webErrors.InvalidDatabaseName)
				default:
					renderError(w, err)
				}
				return
			}
			renderError(w, err)
			return
		}

		render(w, nil, nil)
	})

	return r, nil
}

// Initialize and validate a TimeRange struct with parameters
func newTimeRange(start string, end string) (*database.TimeRange, error) {
	// Return nil if neither start nor end provided
	if len(start) == 0 && len(end) == 0 {
		return nil, nil
	}

	timeRange := database.TimeRange{}

	var startTime time.Time
	var endTime time.Time

	if len(start) > 0 {
		// Try to parse as RFC3339
		startTime, err := time.Parse(time.RFC3339, start)
		if err != nil {
			// Try to parse as RFC1123
			startTime, err = time.Parse(time.RFC1123, start)
			if err != nil {
				return nil, err
			}
		}
		timeRange.Start = startTime
	}

	if len(end) > 0 {
		// Try to parse as RFC3339
		endTime, err := time.Parse(time.RFC3339, end)
		if err != nil {
			// Try to parse as RFC1123
			endTime, err = time.Parse(time.RFC1123, end)
			if err != nil {
				return nil, err
			}
		}
		timeRange.End = endTime
	}

	// Ensure endTime < startTime
	if len(start) > 0 && len(end) > 0 && endTime.Before(startTime) {
		err := errors.New("start must be before end in a TimeRange")
		return nil, err
	}

	return &timeRange, nil
}
