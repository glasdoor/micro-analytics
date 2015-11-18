package database

import (
    "database/sql"
    "log"
)

func Query(db *sql.DB) []Analytic {
    // Query
    query := `SELECT time, type, path, ip, platform, refererDomain, countryCode FROM visits`

    // Exec query
    rows, err := db.Query(query)
    if err != nil {
        log.Fatal("Error querying DB", err)
    }
    defer rows.Close()

    analytics := make([]Analytic, 0)
    for rows.Next() {
        analytic := Analytic{}
        rows.Scan(&analytic.Time,
            &analytic.Type,
            &analytic.Path,
            &analytic.Ip,
            &analytic.Platform,
            &analytic.RefererDomain,
            &analytic.CountryCode)
        analytics = append(analytics, analytic)
    }

    return analytics
}