package location

import "math"

const (
	drivingSpeed float32 = 20 //cheap alternative to GMaps API, 1/20 KMH
)

//CalcDistance will calculate haversine distance between 2 points
func calcDistance(latitude1, longitude1, latitude2, longitude2 float64) float32 {
	const earthRadius float64 = 6371 //Earth radius in km
	latitude1Radian := float64(math.Pi * latitude1 / 180)
	latitude2Radian := float64(math.Pi * latitude2 / 180)

	longitudeDiff := float64(longitude1 - longitude2)
	longitudeDiffRadian := float64(math.Pi * longitudeDiff / 180)

	distanceCos := math.Sin(latitude1Radian)*math.Sin(latitude2Radian) + math.Cos(latitude1Radian)*math.Cos(latitude2Radian)*math.Cos(longitudeDiffRadian)
	if distanceCos > 1 {
		distanceCos = 1
	}

	distance := math.Acos(distanceCos) * earthRadius

	return float32(distance)
}

//CalcDrivingTime will calculate average driving time between 2 locations in hours
func CalcDrivingTime(latitude1, longitude1, latitude2, longitude2 float64) float32 {
	//TODO: Replace with GMaps API
	return calcDistance(latitude1, longitude1, latitude2, longitude2) / drivingSpeed
}
