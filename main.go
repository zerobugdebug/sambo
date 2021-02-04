package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/withmandala/go-log"
)

const (
	workersDBFileName            string = "worker_info.csv"
	tasksDBFileName              string = "task_info.csv"
	projectsDBFileName           string = "project_info.csv"
	projectFamiliarityDBFileName string = "worker_project_hours.csv"
	workersTimeOffDBFileName     string = "worker_time_off.csv"
)

//Genetic algorithm parameters
const (
	populationSize   int     = 1    //size of the population
	generationsLimit int     = 1    //how many generations to generate
	crossoverRate    float32 = 1    //how often to do crossover 0%-100% in decimal
	mutationRate     float32 = 0.25 //how often to do mutation 0%-100% in decimal
	elitismRate      float32 = 0.05 //how many of the best indviduals to keep intact
	deadend          float32 = 8760 //365 days in hours, fitness for the dead end individual, i.e. impossible to assign workers to all the tasks
)

//Worker best fit, weighted decision matrix (AHP)
const (
	weightDistance           float32 = 1
	weightDelay              float32 = 1
	weightProjectFamiliarity float32 = 0.01
	weightDemand             float32 = 1
	maxValueDistance         float32 = 100
	maxValueDelay            float32 = 100
	maxValueDemand           float32 = 1
	//weightTrades             float32 = 1 //for the trades implementation

)

//Additional constants
const (
	drivingSpeed float32 = 0.0333333 //cheap alternative to GMaps API, 1/30 KMH
)

type worker struct {
	name      string
	latitude  float64
	longitude float64
	demand    float32 //how many tasks could be assigned to worker
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
	// valueTrades             float32
	valueDemand float32
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
	name             string
	validWorkers     map[string]struct{} //unique hash map of empty structs to store validWorkers IDs
	project          string
	prerequisites    map[string]float32 //store unique prerequisite and corresponding lag/lead hours
	duration         float32
	idealWorkerCount int
	minWorkerCount   int
	maxWorkerCount   int
}

type scheduledTask struct {
	taskID           string
	startTime        float32
	stopTime         float32
	assignees        []string
	numPrerequisites int
}

//Global variables to act as a in-memory reference DB
//TODO: Replace with some external in memory storage, because global vars are BAD
var tasksDB map[string]task                            //key is the task ID
var workersDB map[string]worker                        //key is the worker ID
var projectsDB map[string]project                      //key is the project ID
var projectFamiliarityDB map[string]map[string]float32 //key1 is the project ID, key2 is the worker ID

var loggerInfo = log.New(os.Stdout).WithDebug()

func (task task) print() {
	fmt.Printf("%+v\n", task)
}

func (task scheduledTask) print() {
	fmt.Printf("%+v\n", task)
}

func (worker scheduledWorker) print() {
	fmt.Printf("%+v\n", worker)
}

func (individual individual) print() {
	fmt.Printf("%+v\n", individual)
}

func readProjectInfoCSV() map[string]project {
	var projectTemp project
	projectsDB := make(map[string]project)
	projectsDBFile, err := os.Open(projectsDBFileName)
	if err != nil {
		loggerInfo.Fatal("Couldn't open the "+projectsDBFileName+" file\r\n", err)
	}
	projectsData := csv.NewReader(projectsDBFile)
	_, err = projectsData.Read() //skip CSV header
	for {
		projectsRecord, err := projectsData.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			loggerInfo.Fatal(err)
		}
		projectTemp.name = projectsRecord[1]
		projectTemp.latitude, err = strconv.ParseFloat(projectsRecord[2], 64)
		if err != nil {
			loggerInfo.Error("Couldn't parse project latitude value", err)
		}
		projectTemp.longitude, err = strconv.ParseFloat(projectsRecord[3], 64)
		if err != nil {
			loggerInfo.Error("Couldn't parse project longitude value", err)
		}
		projectsDB[projectsRecord[0]] = projectTemp
	}
	return projectsDB
}

func readTaskInfoCSV() map[string]task {
	var taskTemp task
	tasksDB := make(map[string]task)
	tasksDBFile, err := os.Open(tasksDBFileName)
	if err != nil {
		loggerInfo.Fatal("Couldn't open the "+tasksDBFileName+" file\r\n", err)
	}
	tasksData := csv.NewReader(tasksDBFile)
	_, err = tasksData.Read() //skip CSV header
	for {
		tasksRecord, err := tasksData.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			loggerInfo.Fatal(err)
		}
		taskTemp.project = tasksRecord[0]
		taskTemp.name = tasksRecord[2]
		taskTemp.validWorkers = make(map[string]struct{})
		for _, v := range strings.Fields(tasksRecord[3]) {
			taskTemp.validWorkers[v] = struct{}{}
		}
		taskTemp.idealWorkerCount, err = strconv.Atoi(tasksRecord[5])

		strings.Fields(tasksRecord[3])
		taskTemp.prerequisites = make(map[string]float32)
		prerequisitesTemp := strings.Fields(tasksRecord[4])
		lagHoursTemp := strings.Fields(tasksRecord[9])
		for i, v := range prerequisitesTemp {
			lagHours, err := strconv.ParseFloat(lagHoursTemp[i], 32)
			if err != nil {
				loggerInfo.Warn("Couldn't parse Lag hours value", err)
				loggerInfo.Warn("Original row: ", tasksRecord)
			}
			taskTemp.prerequisites[taskTemp.project+"."+v] = float32(lagHours)
		}
		tempDuration, err := strconv.ParseFloat(tasksRecord[8], 32)
		if err != nil {
			loggerInfo.Warn("Couldn't parse task duration value", err)
			loggerInfo.Warn("Original row: ", tasksRecord)

		}
		taskTemp.duration = float32(tempDuration)
		tasksDB[taskTemp.project+"."+tasksRecord[1]] = taskTemp
	}
	return tasksDB
}

func readWorkerInfoCSV() map[string]worker {
	var workerTemp worker
	workersDB := make(map[string]worker)
	workersDBFile, err := os.Open(workersDBFileName)
	if err != nil {
		loggerInfo.Fatal("Couldn't open the "+workersDBFileName+" file\r\n", err)
	}
	workersData := csv.NewReader(workersDBFile)
	_, err = workersData.Read() //skip CSV header
	for {
		workersRecord, err := workersData.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			loggerInfo.Fatal(err)
		}
		workerTemp.name = workersRecord[0]
		workerTemp.latitude, err = strconv.ParseFloat(workersRecord[2], 64)
		if err != nil {
			loggerInfo.Warn("Couldn't parse project latitude value", err)
		}
		workerTemp.longitude, err = strconv.ParseFloat(workersRecord[3], 64)
		if err != nil {
			loggerInfo.Warn("Couldn't parse project longitude value", err)
		}
		workersDB[workersRecord[1]] = workerTemp
	}
	return workersDB

}

func readWorkerProjectHoursCSV() map[string]map[string]float32 {
	projectFamiliarityDB := make(map[string]map[string]float32)
	projectFamiliarityDBFile, err := os.Open(projectFamiliarityDBFileName)
	if err != nil {
		loggerInfo.Fatal("Couldn't open the "+projectFamiliarityDBFileName+" file\r\n", err)
	}
	projectFamiliarityData := csv.NewReader(projectFamiliarityDBFile)
	_, err = projectFamiliarityData.Read() //skip CSV header
	for {
		projectFamiliarityRecord, err := projectFamiliarityData.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			loggerInfo.Fatal(err)
		}
		workerProjectHours, err := strconv.ParseFloat(projectFamiliarityRecord[2], 64)
		if err != nil {
			loggerInfo.Warn("Couldn't parse worker project hours", err)
		}
		if _, ok := projectFamiliarityDB[projectFamiliarityRecord[1]]; !ok {
			projectFamiliarityDB[projectFamiliarityRecord[1]] = make(map[string]float32)
		}
		projectFamiliarityDB[projectFamiliarityRecord[1]][projectFamiliarityRecord[0]] = float32(workerProjectHours)
	}
	return projectFamiliarityDB
}

func readWorkerTimeOffCSV() {}

func calculateWorkersDemand() map[string]worker {
	var workerTemp worker
	for _, task := range tasksDB {
		for validWorker := range task.validWorkers {
			workerTemp = workersDB[validWorker]
			workerTemp.demand++
			workersDB[validWorker] = workerTemp
		}
	}
	totalTasks := len(tasksDB)
	for workerID, worker := range workersDB {
		worker.demand = float32(worker.demand) / float32(totalTasks)
		workersDB[workerID] = worker
	}
	return workersDB
}

//Generate individual by randomizing the taskDB
func generateIndividual() individual {
	var newIndividual individual
	taskOrder := rand.Perm(len(tasksDB))
	newIndividual.tasks = make([]scheduledTask, len(tasksDB))
	i := 0
	for k, v := range tasksDB {
		newIndividual.tasks[taskOrder[i]].taskID = k
		newIndividual.tasks[taskOrder[i]].startTime = -1
		newIndividual.tasks[taskOrder[i]].assignees = make([]string, 0)
		newIndividual.tasks[taskOrder[i]].numPrerequisites = len(v.prerequisites)
		i++
	}

	i = 0
	newIndividual.workers = make([]scheduledWorker, len(workersDB))
	for k, v := range workersDB {
		newIndividual.workers[i].workerID = k
		newIndividual.workers[i].canStartIn = 0
		newIndividual.workers[i].latitude = v.latitude
		newIndividual.workers[i].longitude = v.longitude
		newIndividual.workers[i].fitness = 0
		i++
	}

	return newIndividual
}

//Reset individual state
func resetIndividual(individual individual) individual {
	for i, v := range individual.tasks {
		individual.tasks[i].startTime = -1
		individual.tasks[i].assignees = make([]string, 0)
		individual.tasks[i].numPrerequisites = len(tasksDB[v.taskID].prerequisites)
	}

	for i, v := range individual.workers {
		individual.workers[i].canStartIn = 0
		individual.workers[i].latitude = v.latitude
		individual.workers[i].longitude = v.longitude
		individual.workers[i].fitness = 0
	}

	return individual
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

	distance := math.Acos(distanceCos) * earthRadius

	return float32(distance)
}

//Calculate fitness for every worker for the current task
func calculateWorkersFitness(task scheduledTask, workers []scheduledWorker) {
	for i, v := range workers {

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
		//loggerInfo.Debug(v.latitude, v.longitude, projectsDB[tasksDB[task.taskID].project].latitude, projectsDB[tasksDB[task.taskID].project].longitude)

		if valueDistance == 0 {
			valueDistance = maxValueDistance
		} else {
			valueDistance = 1 / valueDistance
		}

		//Fewer tasks can be done by worker => higher number => better fit
		//TODO: Implement recalculation of demand based on the remaining unscheduled tasks
		valueDemand := workersDB[v.workerID].demand
		if valueDemand != 0 {
			valueDemand = 1 / valueDemand
		}

		/*
			//TRADES IMPLEMENTATION
			 		//Fewer trades => higher number => better fit
			   		valueTrades := float32(0)
			   		trades := workersDB[v.workerID].trades
			   		for _, v := range trades {
			   			if v == trade {
			   				valueTrades = float32(1) / float32(len(trades))
			   				break
			   			}
			   		}
		*/
		workers[i].valueDistance = valueDistance
		workers[i].valueProjectFamiliarity = valueProjectFamiliarity
		workers[i].valueDemand = valueDemand
		workers[i].valueDelay = valueDelay
		//v.valueTrades = valueTrades //TRADES IMPLEMENTATION

		//loggerInfo.Debug("Values=", workers[i].workerID, valueDelay, valueProjectFamiliarity, valueDistance, valueDemand)
		//Calculate AHP fitness for the worker, higher number => better fit
		workers[i].fitness = valueDelay*weightDelay + valueProjectFamiliarity*weightProjectFamiliarity + valueDistance*weightDistance + valueDemand*weightDemand
		//loggerInfo.Debug("Normalized=", workers[i].workerID, valueDelay*weightDelay, valueProjectFamiliarity*weightProjectFamiliarity, valueDistance*weightDistance, valueDemand*weightDemand, workers[i].fitness)
		//loggerInfo.Debugf("%v=%v", v.workerID, workers[i].fitness)
		// + valueTrades*weightTrades //TRADES IMPLEMENTATION
	}

}

func assignBestWorker(task scheduledTask, workers []scheduledWorker) (scheduledTask, bool) {

	var workerAssigned bool = false
	//Sort workers in the best fit (descending) order - from largest to smallest
	sort.Slice(workers, func(i, j int) bool {
		return workers[i].fitness > workers[j].fitness
	})
	//loggerInfo.Debug(task)
	//Scan through the workers slice to find the first available worker
	for i, worker := range workers {
		//Assign only if worker can be assigned to this task
		//Check if workerID exists in the validWorkers map in taskDB
		if _, ok := tasksDB[task.taskID].validWorkers[worker.workerID]; ok {
			task.assignees = append(task.assignees, worker.workerID)
			loggerInfo.Debugf("Can be assigned, worker:%v", worker)
			//TODO: Replace with proper calculation and GMaps API
			//startTime should be the earliest of all workers working on the task
			if task.startTime == -1 {
				//Task was never scheduled and task has no predecessors
				task.startTime = workers[i].canStartIn + drivingSpeed/workers[i].valueDistance
			} else if task.stopTime == 0 && task.startTime < workers[i].canStartIn+drivingSpeed/workers[i].valueDistance {
				//Task was never scheduled, but start time defined by predecessors
				task.startTime = workers[i].canStartIn + drivingSpeed/workers[i].valueDistance
			}

			//loggerInfo.Debug(task)
			//Extend stop time if current worker can't finish in time
			if workers[i].canStartIn+drivingSpeed/workers[i].valueDistance+tasksDB[task.taskID].duration > task.stopTime {
				task.stopTime = workers[i].canStartIn + drivingSpeed/workers[i].valueDistance + tasksDB[task.taskID].duration
			}
			//loggerInfo.Debug(task)
			//Change worker's next start time
			workers[i].canStartIn = task.stopTime

			//Change worker's location
			workers[i].latitude = projectsDB[tasksDB[task.taskID].project].latitude
			workers[i].longitude = projectsDB[tasksDB[task.taskID].project].longitude

			//Assign success flag to prevent loops on the calling function
			workerAssigned = true
			//Worker assigned, ignore other workers
			break
		}
	}
	return task, workerAssigned
}

/*
//TRADES IMPLEMENTATION
//Calculate fitness for every worker for the current task WITH TRADES
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
		//		v.valueTrades = valueTrades
		v.valueDelay = valueDelay
		//Calculate AHP fitness for the worker, higher number => better fit
		v.fitness = valueDelay*weightDelay + valueProjectFamiliarity*weightProjectFamiliarity + valueDistance*weightDistance // + valueTrades*weightTrades
	}

}

*/

/*
//TRADES IMPLEMENTATION
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
*/

//Apply crossovers and mutations on non-elite individuals
func transmogrifyPopulation(population []individual) {
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
		population[elitesNum+i] = resetIndividual(population[elitesNum+i])
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
	loggerInfo.Debug("Infinite loop until no workers can be assigned")
	for condition := true; condition; condition = workerAssigned {
		//Prevent loops if no tasks left to process
		workerAssigned = false
		//Loop across all tasks
		for i, task := range individual.tasks {
			//loggerInfo.Debug("taskID =", task.taskID)
			//Process only tasks with remaining worker slots and with all the dependencies met
			if len(task.assignees) < tasksDB[task.taskID].idealWorkerCount && task.numPrerequisites == 0 {
				//Assign workers to the task until idealWorkerCount
				for j := len(individual.tasks[i].assignees); j < tasksDB[task.taskID].idealWorkerCount; j++ {
					loggerInfo.Debug("worker j =", j)
					//Calculate fitness of idealWorkerCount workers for specific task
					//TODO: Add "taint" flag to worker to prevent recalculation of fitness for untouched workers
					calculateWorkersFitness(task, individual.workers)
					loggerInfo.Debug(task)
					//Try to assign worker to task and update worker data
					//TODO: Multiple bool assignments. Any way to make it better?
					individual.tasks[i], workerAssigned = assignBestWorker(task, individual.workers)
					loggerInfo.Debug(individual.tasks[i])
				}
				//Modify dependant tasks if idealWorkerCount workers are scheduled
				if len(individual.tasks[i].assignees) == tasksDB[task.taskID].idealWorkerCount {
					prerequisiteTask := individual.tasks[i]
					//Loop over all tasks
					for i, task := range individual.tasks {
						if task.numPrerequisites > 0 {
							//Check if prerequisiteTask.taskID exists in the prerequisites map in tasksDB
							if _, ok := tasksDB[task.taskID].prerequisites[prerequisiteTask.taskID]; ok {
								//Remove this task from prerequisites for all other tasks
								individual.tasks[i].numPrerequisites--
								//Update task.startTime to match predecessor stop time and account for lag/lead hours
								if individual.tasks[i].startTime < prerequisiteTask.stopTime+tasksDB[task.taskID].prerequisites[prerequisiteTask.taskID] {
									individual.tasks[i].startTime = prerequisiteTask.stopTime + tasksDB[task.taskID].prerequisites[prerequisiteTask.taskID]
								}

							}

						}

					}
				}
			}
		}
	}

	for _, task := range individual.tasks {
		//If we have tasks/trades with no workers assigned, the individual is a dead end
		if len(task.assignees) != tasksDB[task.taskID].idealWorkerCount {
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

/*
//TRADES IMPLEMENTATION
//Generate individual schedule and calculate fitness WITH TRADES (future version)
//func generateIndividualScheduleWithTrades(individual individual) individual {

	//var workerAssigned bool = true
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
*/
//Calculate viability and fitness

/* 	for _, task := range individual.tasks {
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
*/

func main() {

	var population []individual

	rand.Seed(time.Now().UnixNano())

	//projectsDB = make(map[string]project)
	//projectsDB, projectFamiliarityDB, tasksDB, workersDB, workersTimeOffDB = readCSVs()

	//Global DB vars can be accessed directly, but to follow the standard approach used as a func output
	projectsDB = readProjectInfoCSV()
	tasksDB = readTaskInfoCSV()
	workersDB = readWorkerInfoCSV()
	projectFamiliarityDB = readWorkerProjectHoursCSV()
	readWorkerTimeOffCSV()

	workersDB = calculateWorkersDemand() //not neeeded if trades would be implemented
	//projectsDB = readProjectInfoCSV()
	//fmt.Println(projectsDB)
	//fmt.Println(tasksDB)
	//fmt.Println(workersDB)
	//fmt.Println(projectFamiliarityDB)
	population = generatePopulation()

	for i := 0; i < generationsLimit; i++ {
		//Generate schedule and calculate fitness
		generatePopulationSchedules(population)
		//Sort population in the fitness order
		sortPopulation(population)
		fmt.Println("Generation ", i)
		for _, v := range population[0].tasks {
			fmt.Println(v)
		}

		fmt.Println(population[0].fitness)
		//Mutate and crossover population
		transmogrifyPopulation(population)
	}
}
