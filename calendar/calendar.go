package calendar

import (
	"math"
	"time"
)

const timeRoundingSeconds float32 = 600

//Site is a struct to store the working site time limitations
type Site struct {
	DailyStartTime time.Time
	DailyEndTime   time.Time
	Holidays       map[time.Time]struct{}
	LunchStartTime time.Time
	LunchEndTime   time.Time
}

//AddHours will add number of hours to the startTime, according to the Site working time limitation, holidays and weekends
func (site Site) AddHours(startTime time.Time, hours float32) time.Time {
	//TODO: Account for lunch hours
	//TODO: Can break if start time is on the weekend or holiday

	seconds := float64(hours * 3600)

	//Number of working hours per day
	workingHoursPerDay := site.DailyEndTime.Sub(site.DailyStartTime).Hours()
	//Number of days required to finish work without holidays or weekends. 0.0001 (~0.4 seconds) to fix the edge cases, e.g. 8 hrs in 8 hrs working day
	totalDays := int(math.Floor(float64(hours-0.0001) / workingHoursPerDay))
	//End of current working day
	todayEndTime := time.Date(startTime.Year(), startTime.Month(), startTime.Day(), site.DailyEndTime.Hour(), site.DailyEndTime.Minute(), site.DailyEndTime.Second(), 0, startTime.Location())
	//Account for the possible overflow of work to the next day, e.g. 4 hours work start at 15:00
	if startTime.Add(time.Duration(seconds-float64(totalDays)*workingHoursPerDay*3600) * time.Second).After(todayEndTime) {
		totalDays++
	}

	//Calculated end time of work with days only
	endTime := time.Date(startTime.Year(), startTime.Month(), startTime.Day(), 0, 0, 0, 0, startTime.Location())

	//Count required number of working days, skipping weekends and hoildays
	var workingDays int = 0
	for workingDays < totalDays {
		endTime = endTime.AddDate(0, 0, 1)
		if endTime.Weekday() == time.Saturday {
			endTime = endTime.AddDate(0, 0, 2)
		} else if _, ok := site.Holidays[endTime]; ok {
			endTime = endTime.AddDate(0, 0, 1)
		} else {
			workingDays++
		}
	}

	//Remaining hours of work on the last day in seconds
	remainingSeconds := 3600 * (float64(hours) - float64(totalDays-1)*workingHoursPerDay - todayEndTime.Sub(startTime).Hours())
	//Shift endTime to the correct hours
	endTime = endTime.Add(time.Duration(remainingSeconds) * time.Second)

	//Round to timeRounding minutes
	endTime = endTime.Round(time.Duration(timeRoundingSeconds) * time.Second)

	return endTime
}
