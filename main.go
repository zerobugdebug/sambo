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
	populationSize   int     = 5    //size of the population
	generationsLimit int     = 100  //how many generations to generate
	crossoverRate    float32 = 1    //how often to do crossover 0%-100% in decimal
	mutationRate     float32 = 0.25 //how often to do mutation 0%-100% in decimal
	elitismRate      float32 = 0.05 //how many of the best indviduals to keep intact
	deadend          float32 = 8760 //365 days in hours, fitness for the dead end individual, e.g. impossible to assign workers to all the tasks
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
	name          string
	trades        []string
	project       string
	prerequisites map[string]struct{} //unique array to prevent duplication of the prerequisites
	duration      float32
}

type scheduledTask struct {
	taskID           string
	startTime        float32
	stopTime         float32
	assignees        []string
	numPrerequisites int
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
	for k, v := range tasksDB {
		newIndividual.tasks[taskOrder[i]].taskID = k
		newIndividual.tasks[taskOrder[i]].startTime = 0
		newIndividual.tasks[taskOrder[i]].assignees = make([]string, 0)
		newIndividual.tasks[taskOrder[i]].numPrerequisites = len(v.prerequisites)
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
	const earthRadius float64 = 6371 //Earth radius in km
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

		//Smaller wait time => higher number => better fit
		valueDelay := v.canStartIn
		if valueDelay == 0 {
			valueDelay = maxValueDelay
		} else {
			valueDelay = 1 / valueDelay
		}

		//More hours in project => higher number => better fit
		valueProjectFamiliarity := projectFamiliarityDB[tasksDB[task.taskID].project][v.workerID]

		//Shorter distance => higher number => better fit
		valueDistance := calcDistance(v.latitude, v.longitude, projectsDB[tasksDB[task.taskID].project].latitude, projectsDB[tasksDB[task.taskID].project].longitude)
		if valueDistance == 0 {
			valueDistance = maxValueDistance
		} else {
			valueDistance = 1 / valueDistance
		}

		//Fewer trades => higher number => better fit
		valueTrades := float32(0)
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
		//Calculate AHP fitness for the worker, higher number => better fit
		v.fitness = valueDelay*weightDelay + valueProjectFamiliarity*weightProjectFamiliarity + valueDistance*weightDistance + valueTrades*weightTrades
	}

}

func assignBestWorker(task scheduledTask, workers []scheduledWorker) (scheduledTask, bool) {

	var workerAssigned bool = false
	//Sort workers in the best fit (descending) order - from largest to smallest
	sort.Slice(workers, func(i, j int) bool {
		return workers[i].fitness > workers[j].fitness
	})
	for i, v := range workers {
		//Assign only if worker has required trade
		if v.valueTrades != 0 {
			task.assignees = append(task.assignees, workers[i].workerID)
			//TODO: Replace with proper calculation and GMaps API
			task.startTime = workers[0].canStartIn + drivingSpeed/workers[i].valueDistance

			//Keep stop time intact for the multiple trades with different availability
			if task.stopTime-task.startTime < tasksDB[task.taskID].duration {
				task.stopTime = task.startTime + tasksDB[task.taskID].duration
			}
			//Change worker's next start time
			workers[i].canStartIn = task.startTime + tasksDB[task.taskID].duration

			//Change worker's location
			workers[i].latitude = projectsDB[task.taskID].latitude
			workers[i].longitude = projectsDB[task.taskID].longitude

			//Assign success flag to prevent loops on the calling function
			workerAssigned = true
			//Worker assigned, ignore other workers
			break
		}
	}
	return task, workerAssigned
}

//Apply crossovers and mutations on non-elite individuals
func transmogrifyPopulation() {
	elitesNum := int(elitismRate * float32(len(population)))
	for i := range population[elitesNum:] {
		//Do crossover for some indviduals
		if rand.Float32() < crossoverRate {
			i1 := elitesNum + i
			//Randomly select second parent from all individuals after the current one
			i2 := i1 + rand.Intn(len(population)-i1)
			//TODO: Slice will be modified in place, need to check
			population[i1], population[i2] = crossoverIndividuals(population[i1], population[i2])
		}
		//Do mutation for some indviduals
		if rand.Float32() < mutationRate {
			//TODO: Slice will be modified in place, need to check
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

func sortPopulation(population []individual) {
	//Sort indviduals in the order of fitness (ascending) - from smallest to largest
	sort.Slice(population, func(i, j int) bool {
		return population[i].fitness < population[j].fitness
	})
}

func generatePopulationSchedules(population []individual) {
	//TODO: Slice will be modified in place, need to check
	for i := range population {
		population[i] = generateIndividualSchedule(population[i])
	}
}

//Generate individual schedule and calculate fitness
func generateIndividualSchedule(individual individual) individual {

	var workerAssigned bool = true
	//Infinite loop until no workers can be assigned
	for condition := true; condition; condition = workerAssigned {
		//Prevent loops if no tasks left to process
		workerAssigned = false
		//Loop across all tasks
		for i, task := range individual.tasks {
			//Process only tasks with remaining trades and with all the dependencies met
			if len(task.assignees) < len(tasksDB[task.taskID].trades) && task.numPrerequisites == 0 {
				for _, trade := range tasksDB[task.taskID].trades {
					//Calculate fitness of all workers for specific task and trade
					//TODO: Add "taint" flag to worker to prevent recalculation of fitness for untouched workers
					calculateWorkersFitness(task, trade, individual.workers)
					//Try to assign worker to task and update worker data
					//TODO: Multiple bool assignments. Any way to make it better?
					individual.tasks[i], workerAssigned = assignBestWorker(task, individual.workers)
					/* 					if maxStopTime < individual.tasks[i].stopTime {
						maxStopTime = individual.tasks[i].stopTime
					} */
				}
				//Remove this task from prerequisites for all other tasks if all trades are scheduled
				if len(task.assignees) == len(tasksDB[task.taskID].trades) {
					prerequisiteID := task.taskID
					//Loop over all tasks
					for i, task := range individual.tasks {
						if task.numPrerequisites > 0 {
							//Check if prerequisiteID exists in the prerequisites map in taskDB
							if _, ok := tasksDB[task.taskID].prerequisites[prerequisiteID]; ok {
								individual.tasks[i].numPrerequisites--
							}
						}
					}
				}
			}
		}
	}

	//Calculate viability and fitness

	for _, task := range individual.tasks {
		//If we have tasks/trades with no workers assigned, the individual is a dead end
		if len(task.assignees) != len(tasksDB[task.taskID].trades) {
			individual.fitness = deadend
			break
		}
		//Earlier stopTime => faster we finish all the tasks => better individual fitness
		if individual.fitness < task.stopTime {
			individual.fitness = task.stopTime
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
		//Generate schedule and calculate fitness
		generatePopulationSchedules(population)
		//Sort population in the fitness order
		sortPopulation(population)
		//Mutate and crossover population
		transmogrifyPopulation()
	}
}
