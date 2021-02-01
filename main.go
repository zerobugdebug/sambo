package main

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"
)

//Genetic algorithm parameters
const (
	populationSize   int     = 5
	generationsLimit int     = 100
	crossoverRate    float32 = 1
	elitismRate      float32 = 0.05
	mutationRate     float32 = 0.25
)

//Worker best fit, weighted decision matrix (AHP)
const (
	weightDistance           float32 = 0.1
	weightTrades             float32 = 1
	weightDelay              float32 = 0.01
	weightProjectFamiliarity float32 = 0.5
	maxValueDistance         float32 = 100
	maxValueDelay            float32 = 100
)

//Additional constants
const (
	drivingSpeed float32 = 10 //Cheap alternative to GMaps API
)

var allowedTrades = [...]string{"Painter", "Lead", "Helper"}

type worker struct {
	name      string
	trades    []string
	latitude  float64
	longitude float64
}

type scheduledWorker struct {
	workerID                string
	canStartIn              float32
	latitude                float64
	longitude               float64
	fitness                 float32
	valueDelay              float32
	valueDistance           float32
	valueProjectFamiliarity float32
	valueTrades             float32
}

type project struct {
	name      string
	latitude  float64
	longitude float64
}

type individual struct {
	tasks   []scheduledTask
	workers []scheduledWorker
	fitness float32
}
type task struct {
	name         string
	trades       []string
	project      string
	dependencies []string
	duration     float32
}

type scheduledTask struct {
	taskID    string
	startTime float32
	stopTime  float32
	assignees []string
}

var tasksDB map[string]task                            //key is the task ID
var workersDB map[string]worker                        //key is the worker ID
var projectsDB map[string]project                      //key is the project ID
var projectFamiliarityDB map[string]map[string]float32 //key1 is the project ID, key2 is the worker ID
var population []individual

func (task task) print() {
	fmt.Printf("%+v\n", task)
}

func (individual individual) print() {
	fmt.Printf("%+v\n", individual)
}

func readProjectInfoCSV()        {}
func readTaskInfoCSV()           {}
func readWorkerInfoCSV()         {}
func readWorkerProjectHoursCSV() {}
func readWorkerTimeOffCSV()      {}

func readCSVs() map[string]task {
	//taskList := []task{{1, "abc", 0, 0, ""}, {2, "def", 0, 0, ""}, {3, "ghi", 0, 0, ""}}
	//	taskList[0] = task{1, "abc", 0, 0, ""}
	//	taskList[1] = task{2, "def", 0, 0, ""}
	//	taskList[2] = task{3, "ghi", 0, 0, ""}
	tasks := make(map[string]task)
	readProjectInfoCSV()
	readTaskInfoCSV()
	readWorkerInfoCSV()
	readWorkerProjectHoursCSV()
	readWorkerTimeOffCSV()
	return tasks

}

//Generate individual by randomizing the taskDB
func generateIndividual() individual {
	var newIndividual individual
	taskOrder := rand.Perm(len(tasksDB))
	newIndividual.tasks = make([]scheduledTask, len(tasksDB))
	i := 0
	for k := range tasksDB {
		newIndividual.tasks[taskOrder[i]].taskID = k
		newIndividual.tasks[taskOrder[i]].startTime = 0
		newIndividual.tasks[taskOrder[i]].assignees = make([]string, 0)
		i++
	}

	return newIndividual
}

func generatePopulation() []individual {
	var population []individual
	for i := 0; i < populationSize; i++ {
		individual := generateIndividual()
		population = append(population, individual)
	}
	return population
}

//Calculate haversine distance between 2 points
func calcDistance(latitude1, longitude1, latitude2, longitude2 float64) float32 {
	const earthRadius = 6371 //Earth radius in km
	latitude1Radian := float64(math.Pi * latitude1 / 180)
	latitude2Radian := float64(math.Pi * latitude2 / 180)

	longitudeDiff := float64(longitude1 - longitude2)
	longitudeDiffRadian := float64(math.Pi * longitudeDiff / 180)

	distanceCos := math.Sin(latitude1Radian)*math.Sin(latitude2Radian) + math.Cos(latitude1Radian)*math.Cos(latitude2Radian)*math.Cos(longitudeDiffRadian)
	if distanceCos > 1 {
		distanceCos = 1
	}

	distance := distanceCos * earthRadius

	return float32(distance)
}

//Calculate fitness for every worker for the current task
func calculateWorkersFitness(task scheduledTask, trade string, workers []scheduledWorker) {
	for _, v := range workers {

		valueDelay := v.canStartIn
		if valueDelay == 0 {
			valueDelay = maxValueDelay
		} else {
			valueDelay = 1 / valueDelay
		} //smaller wait time => higher number => better fit

		valueProjectFamiliarity := projectFamiliarityDB[tasksDB[task.taskID].project][v.workerID] //more hours in project => higher number => better fit

		valueDistance := calcDistance(v.latitude, v.longitude, projectsDB[tasksDB[task.taskID].project].latitude, projectsDB[tasksDB[task.taskID].project].longitude)
		if valueDistance == 0 {
			valueDistance = maxValueDistance
		} else {
			valueDistance = 1 / valueDistance
		} //shorter distance => higher number => better fit

		valueTrades := float32(0) //fewer trades => higher number => better fit

		trades := workersDB[v.workerID].trades
		for _, v := range trades {
			if v == trade {
				valueTrades = float32(1) / float32(len(trades))
				break
			}
		}

		v.valueDistance = valueDistance
		v.valueProjectFamiliarity = valueProjectFamiliarity
		v.valueTrades = valueTrades
		v.valueDelay = valueDelay
		v.fitness = valueDelay*weightDelay + valueProjectFamiliarity*weightProjectFamiliarity + valueDistance*weightDistance + valueTrades*weightTrades //higher number => better fit
	}

}

func assignBestWorker(task scheduledTask, workers []scheduledWorker) scheduledTask {
	sort.Slice(workers, func(i, j int) bool {
		return workers[i].fitness > workers[j].fitness
	})
	for i, v := range workers {
		if v.valueTrades != 0 {
			task.assignees = append(task.assignees, workers[i].workerID)
			task.startTime = workers[0].canStartIn + drivingSpeed/workers[i].valueDistance //TODO: Replace with proper calculation and GMaps API

			if task.stopTime-task.startTime < tasksDB[task.taskID].duration { //keep stop time intact for the multiple trades with different availability
				task.stopTime = task.startTime + tasksDB[task.taskID].duration
			}
			workers[i].canStartIn = task.startTime + tasksDB[task.taskID].duration
			workers[i].latitude = projectsDB[task.taskID].latitude
			workers[i].longitude = projectsDB[task.taskID].longitude
			break
		}
	}
	return task
}

func transmogrifyPopulation() {
	elitesNum := int(elitismRate * float32(len(population)))
	for i := range population[elitesNum:] {
		if rand.Float32() < crossoverRate { //let's do crossover
			i1 := elitesNum + i
			i2 := i1 + rand.Intn(len(population)-i1) //randomly selected from all individuals after the current one
			population[i1], population[i2] = crossoverIndividuals(population[i1], population[i2])
		}
		if rand.Float32() < mutationRate { //let's do mutation
			population[elitesNum+i] = mutateIndividual(population[elitesNum+i])
		}
	}
}

func mutateIndividual(individual individual) individual {
	return individual
}

func crossoverIndividuals(individual1, individual2 individual) (individual, individual) {
	return individual1, individual2
}

func calculateIndividualFitness() {

}

func sortPopulation(population []individual) {
	sort.Slice(population, func(i, j int) bool {
		return population[i].fitness < population[j].fitness
	})
}

func generatePopulationSchedules(population []individual) {
	for i := range population {
		population[i] = generateIndividualSchedule(population[i])
	}
}

func generateIndividualSchedule(individual individual) individual {
	for i, task := range individual.tasks {
		for _, trade := range tasksDB[task.taskID].trades {
			calculateWorkersFitness(task, trade, individual.workers)
			individual.tasks[i] = assignBestWorker(task, individual.workers)
		}
	}
	return individual

}

func main() {
	rand.Seed(time.Now().UnixNano())

	projectsDB = make(map[string]project)

	tasksDB = readCSVs()
	population = generatePopulation()
	for i := 0; i < generationsLimit; i++ {
		generatePopulationSchedules(population)
		calculateIndividualFitness()
		sortPopulation(population)
		transmogrifyPopulation()
	}
}
