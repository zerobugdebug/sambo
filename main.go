package main

import (
	"encoding/csv"
	"io"
	"math"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/withmandala/go-log"
	"gitlab.com/alex.skylight/sambo/helpers"
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
	populationSize         int     = 100  //size of the population
	generationsLimit       int     = 10   //how many generations to generate
	crossoverRate          float32 = 1    //how often to do crossover 0%-100% in decimal
	mutationRate           float32 = 1    //how often to do mutation 0%-100% in decimal
	elitismRate            float32 = 0.1  //how many of the best indviduals to keep intact
	deadend                float32 = 8760 //365 days in hours, fitness for the dead end individual, i.e. impossible to assign workers to all the tasks
	tourneySampleSize      int     = 10   //sample size for the tournament selection, should be less than population size
	crossoverParentsNumber int     = 2    //number of parents for the crossover
	maxCrossoverLength     int     = 10   //max number of sequential tasks to cross between individuals
	maxMutatedGenes        int     = 10   //maximum number of mutated genes, min=2
	mutationTypePreference float32 = 0    //prefered mutation type rate. 0 = 100% swap mutation, 1 = 100% displacement mutation
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
	drivingSpeed          float32 = 0.05               //cheap alternative to GMaps API, 1/20 KMH
	defaultDateFormat     string  = "2006-01-02"       //format of date in the csv files
	defaultTimeFormat     string  = "15:04"            //format of time in the csv files
	defaultDateTimeFormat string  = "2006-01-02T15:04" //format of datetime in the csv files
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
	name            string
	latitude        float64
	longitude       float64
	targetStartDate time.Time
	targetEndDate   time.Time
	dailyStartTime  time.Time
	dailyEndTime    time.Time
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
	pinnedDateTime   time.Time
	pinnedWorkerIDs  map[string]struct{}
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

var logger = log.New(os.Stdout).WithoutDebug().WithColor()

func readProjectInfoCSV() map[string]project {
	var projectTemp project
	projectsDB := make(map[string]project)
	projectsDBFile, err := os.Open(projectsDBFileName)
	if err != nil {
		logger.Fatal("Couldn't open the "+projectsDBFileName+" file\r\n", err)
	}
	projectsData := csv.NewReader(projectsDBFile)
	_, err = projectsData.Read() //skip CSV header
	for {
		projectsRecord, err := projectsData.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			logger.Fatal(err)
		}
		projectTemp.name = projectsRecord[1]
		projectTemp.latitude, err = strconv.ParseFloat(projectsRecord[2], 64)
		if err != nil {
			logger.Error("Original record: ", projectsRecord)
			logger.Fatal("Couldn't parse project latitude value", err)
		}
		projectTemp.longitude, err = strconv.ParseFloat(projectsRecord[3], 64)
		if err != nil {
			logger.Error("Original record: ", projectsRecord)
			logger.Fatal("Couldn't parse project longitude value", err)
		}
		projectTemp.targetStartDate, err = time.Parse(defaultDateFormat, projectsRecord[5])
		if err != nil {
			logger.Error("Original record: ", projectsRecord)
			logger.Fatal("Couldn't parse project target start date value", err)
		}
		projectTemp.targetEndDate, err = time.Parse(defaultDateFormat, projectsRecord[6])
		if err != nil {
			logger.Error("Original record: ", projectsRecord)
			logger.Fatal("Couldn't parse project target end date value", err)
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
		logger.Fatal("Couldn't open the "+tasksDBFileName+" file\r\n", err)
	}
	tasksData := csv.NewReader(tasksDBFile)
	_, err = tasksData.Read() //skip CSV header
	for {
		tasksRecord, err := tasksData.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			logger.Fatal(err)
		}
		taskTemp.project = tasksRecord[0]
		taskTemp.name = tasksRecord[2]

		taskTemp.validWorkers = make(map[string]struct{})
		for _, v := range strings.Fields(tasksRecord[3]) {
			taskTemp.validWorkers[v] = struct{}{}
		}

		taskTemp.idealWorkerCount, err = strconv.Atoi(tasksRecord[5])
		if err != nil {
			logger.Error("Original record: ", tasksRecord)
			logger.Fatal("Couldn't parse ideal worker count", err)
		}

		taskTemp.prerequisites = make(map[string]float32)
		prerequisitesTemp := strings.Fields(tasksRecord[4])
		lagHoursTemp := strings.Fields(tasksRecord[9])
		for i, v := range prerequisitesTemp {
			lagHours, err := strconv.ParseFloat(lagHoursTemp[i], 32)
			if err != nil {
				logger.Error("Original record: ", tasksRecord)
				logger.Fatal("Couldn't parse lag hours value", err)
			}
			taskTemp.prerequisites[taskTemp.project+"."+v] = float32(lagHours)
		}

		tempDuration, err := strconv.ParseFloat(tasksRecord[8], 32)
		if err != nil {
			logger.Error("Original record: ", tasksRecord)
			logger.Fatal("Couldn't parse task duration value", err)
		}
		taskTemp.duration = float32(tempDuration)

		if tasksRecord[10] != "" {
			taskTemp.pinnedDateTime, err = time.Parse(defaultDateTimeFormat, tasksRecord[10])
			if err != nil {
				logger.Error("Original record: ", tasksRecord)
				logger.Fatal("Couldn't parse task pinned datetime value", err)
			}
		}

		taskTemp.pinnedWorkerIDs = make(map[string]struct{})
		for _, v := range strings.Fields(tasksRecord[11]) {
			taskTemp.pinnedWorkerIDs[v] = struct{}{}
		}

		tasksDB[taskTemp.project+"."+tasksRecord[1]] = taskTemp
	}
	return tasksDB
}

func readWorkerInfoCSV() map[string]worker {
	var workerTemp worker
	workersDB := make(map[string]worker)
	workersDBFile, err := os.Open(workersDBFileName)
	if err != nil {
		logger.Fatal("Couldn't open the "+workersDBFileName+" file\r\n", err)
	}
	workersData := csv.NewReader(workersDBFile)
	_, err = workersData.Read() //skip CSV header
	for {
		workersRecord, err := workersData.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			logger.Fatal(err)
		}
		workerTemp.name = workersRecord[0]
		workerTemp.latitude, err = strconv.ParseFloat(workersRecord[2], 64)
		if err != nil {
			logger.Error("Original record: ", workersRecord)
			logger.Fatal("Couldn't parse worker longitude value", err)
		}
		workerTemp.longitude, err = strconv.ParseFloat(workersRecord[3], 64)
		if err != nil {
			logger.Error("Original record: ", workersRecord)
			logger.Fatal("Couldn't parse worker longitude value", err)
		}
		workersDB[workersRecord[1]] = workerTemp
	}
	return workersDB

}

func readWorkerProjectHoursCSV() map[string]map[string]float32 {
	projectFamiliarityDB := make(map[string]map[string]float32)
	projectFamiliarityDBFile, err := os.Open(projectFamiliarityDBFileName)
	if err != nil {
		logger.Fatal("Couldn't open the "+projectFamiliarityDBFileName+" file\r\n", err)
	}
	projectFamiliarityData := csv.NewReader(projectFamiliarityDBFile)
	_, err = projectFamiliarityData.Read() //skip CSV header
	for {
		projectFamiliarityRecord, err := projectFamiliarityData.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			logger.Fatal(err)
		}
		workerProjectHours, err := strconv.ParseFloat(projectFamiliarityRecord[2], 64)
		if err != nil {
			logger.Error("Original record: ", projectFamiliarityRecord)
			logger.Fatal("Couldn't parse worker hours value", err)
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
		individual.tasks[i].stopTime = 0
		individual.tasks[i].assignees = make([]string, 0)
		individual.tasks[i].numPrerequisites = len(tasksDB[v.taskID].prerequisites)
	}

	for i, v := range individual.workers {
		individual.workers[i].canStartIn = 0
		individual.workers[i].latitude = workersDB[v.workerID].latitude
		individual.workers[i].longitude = workersDB[v.workerID].longitude
		individual.workers[i].fitness = 0
		individual.workers[i].valueDelay = 0
		individual.workers[i].valueDemand = 0
		individual.workers[i].valueDistance = 0
		individual.workers[i].valueProjectFamiliarity = 0
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
		valueDistance := helpers.CalcDistance(v.latitude, v.longitude, projectsDB[tasksDB[task.taskID].project].latitude, projectsDB[tasksDB[task.taskID].project].longitude)
		//logger.Debug(v.latitude, v.longitude, projectsDB[tasksDB[task.taskID].project].latitude, projectsDB[tasksDB[task.taskID].project].longitude)

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

		//logger.Debug("Values=", workers[i].workerID, valueDelay, valueProjectFamiliarity, valueDistance, valueDemand)
		//Calculate AHP fitness for the worker, higher number => better fit
		workers[i].fitness = valueDelay*weightDelay + valueProjectFamiliarity*weightProjectFamiliarity + valueDistance*weightDistance + valueDemand*weightDemand
		//logger.Debug("Normalized=", workers[i].workerID, valueDelay*weightDelay, valueProjectFamiliarity*weightProjectFamiliarity, valueDistance*weightDistance, valueDemand*weightDemand, workers[i].fitness)
		//logger.Debugf("%v=%v", v.workerID, workers[i].fitness)
		// + valueTrades*weightTrades //TRADES IMPLEMENTATION
	}

}

func assignBestWorker(task scheduledTask, workers []scheduledWorker) (scheduledTask, bool) {

	var workerAssigned bool = false
	//Sort workers in the best fit (descending) order - from largest to smallest
	sort.Slice(workers, func(i, j int) bool {
		return workers[i].fitness > workers[j].fitness
	})
	//logger.Debug(task)
	//Scan through the workers slice to find the first available worker
	for i, worker := range workers {
		//Assign only if worker can be assigned to this task
		//Check if workerID exists in the validWorkers map in taskDB
		if _, ok := tasksDB[task.taskID].validWorkers[worker.workerID]; ok {
			task.assignees = append(task.assignees, worker.workerID)
			//logger.Debugf("Can be assigned, worker:%v", worker)
			//TODO: Replace with proper calculation and GMaps API
			//startTime should be the earliest of all workers working on the task
			if task.startTime == -1 {
				//Task was never scheduled and task has no predecessors
				task.startTime = workers[i].canStartIn + drivingSpeed/workers[i].valueDistance
			} else if task.stopTime == 0 && task.startTime < workers[i].canStartIn+drivingSpeed/workers[i].valueDistance {
				//Task was never scheduled, but start time defined by predecessors
				task.startTime = workers[i].canStartIn + drivingSpeed/workers[i].valueDistance
			}

			//logger.Debug(task)
			//Extend stop time if current worker can't finish in time
			if workers[i].canStartIn+drivingSpeed/workers[i].valueDistance+tasksDB[task.taskID].duration > task.stopTime {
				task.stopTime = workers[i].canStartIn + drivingSpeed/workers[i].valueDistance + tasksDB[task.taskID].duration
			}
			//logger.Debug(task)
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

func copyIndividual(oldIndividual individual) individual {
	var newIndividual individual
	newIndividual.tasks = make([]scheduledTask, len(oldIndividual.tasks))
	copy(newIndividual.tasks, oldIndividual.tasks)
	newIndividual.workers = make([]scheduledWorker, len(oldIndividual.workers))
	copy(newIndividual.workers, oldIndividual.workers)
	return newIndividual
}

func copyPopulation(oldPopulation []individual) []individual {
	var newPopulation []individual
	for _, v := range oldPopulation {
		newPopulation = append(newPopulation, copyIndividual(v))
	}
	return newPopulation
}

//Apply crossovers and mutations on non-elite individuals
func transmogrifyPopulation(population []individual) []individual {
	elitesNum := int(elitismRate * float32(len(population)))
	//logger.Info("elitesNum=", elitesNum)
	var newPopulation []individual
	var tempPopulation []individual
	//Keep elites in the new population
	//	newPopulation = population[:elitesNum]
	newPopulation = copyPopulation(population[:elitesNum])
	logger.Debug("newPopulation size with elites =", len(newPopulation))
	logger.Debug("Best elite fitness =", newPopulation[0].fitness)
	//loggerFile.Info("ELITES:", newPopulation[0].tasks)
	remainingIndividualsNumber := len(population) - elitesNum
	logger.Debug("remainingIndividualsNumber =", remainingIndividualsNumber)
	//Generate len(population)-elitesNum additonal individuals
	for condition := true; condition; condition = remainingIndividualsNumber > 0 {
		tempPopulation = make([]individual, crossoverParentsNumber)
		//Select crossoverParentsNumber from the popualtion with Torunament Selection
		tempPopulation = tourneySelect(population, crossoverParentsNumber)
		logger.Debug("tempPopulation size after tourney =", len(tempPopulation))
		//Apply crossover to the tempPopulation
		tempPopulation = crossoverIndividuals(tempPopulation)
		logger.Debug("tempPopulation size after crossover =", len(tempPopulation))
		//Apply mutation to the tempPopulation
		tempPopulation = mutateIndividuals(tempPopulation)
		logger.Debug("tempPopulation size after mutation =", len(tempPopulation))
		//Append tempPopulation to the new population
		newPopulation = append(newPopulation, copyPopulation(tempPopulation)...)
		logger.Debug("newPopulation size =", len(newPopulation))
		//Update remaining number of individuals to generate
		remainingIndividualsNumber -= crossoverParentsNumber
		logger.Debug("remainingIndividualsNumber =", remainingIndividualsNumber)
		logger.Debug("condition =", condition)
	}
	for i := range newPopulation {
		//loggerFile.Infof("newPopulation[%v].tasks[0] before reset =", i, newPopulation[i].tasks[0])
		//loggerFile.Infof("newPopulation[%v].tasks[1] before reset =", i, newPopulation[i].tasks[1])
		//loggerFile.Infof("newPopulation[%v].tasks[2] before reset =", i, newPopulation[i].tasks[2])
		newPopulation[i] = resetIndividual(newPopulation[i])
		//loggerFile.Infof("newPopulation[%v].tasks[0] after reset =", i, newPopulation[i].tasks[0])
		//loggerFile.Infof("newPopulation[%v].tasks[1] after reset =", i, newPopulation[i].tasks[1])
		//loggerFile.Infof("newPopulation[%v].tasks[2] after reset =", i, newPopulation[i].tasks[2])
	}
	return newPopulation
}

//Tournament selection for the crossover
func tourneySelect(population []individual, number int) []individual {
	//Create slice of randmoly permutated individuals numbers
	sampleOrder := rand.Perm(len(population))
	logger.Debug("sampleOrder =", sampleOrder)

	var bestIndividuals []individual
	var bestIndividualNumber int
	var sampleOrderNumber int
	var bestIndividualFitness float32
	for i := 0; i < number; i++ {
		logger.Debug("Processing individual =", i)

		bestIndividualNumber = 0
		sampleOrderNumber = 0
		bestIndividualFitness = float32(math.MaxFloat32)
		//Select best individual number from first tourneySampleSize elements in sampleOrder
		for j, v := range sampleOrder[:tourneySampleSize] {
			logger.Debugf("Processing sample %v, sample value %v", j, v)
			if population[v].fitness < bestIndividualFitness {
				bestIndividualNumber = v
				bestIndividualFitness = population[v].fitness
				sampleOrderNumber = j
				logger.Debug("bestIndividualNumber =", bestIndividualNumber)
				logger.Debug("bestIndividualFitness =", bestIndividualFitness)
				logger.Debug("sampleOrderNumber =", sampleOrderNumber)

			}
		}
		//Add best individual to return slice
		bestIndividuals = append(bestIndividuals, population[bestIndividualNumber])
		logger.Debug("bestIndividuals size =", len(bestIndividuals))

		//Remove best individual number from the selection
		//Using copy-last&truncate algorithm, due to O(1) complexity
		sampleOrder[sampleOrderNumber] = sampleOrder[len(sampleOrder)-1]
		sampleOrder = sampleOrder[:len(sampleOrder)-1]
		//Shuffle remaining individual numbers
		rand.Shuffle(len(sampleOrder), func(i, j int) { sampleOrder[i], sampleOrder[j] = sampleOrder[j], sampleOrder[i] })
		logger.Debug("new sampleOrder =", sampleOrder)

	}
	return bestIndividuals
}

func displacementMutation(individual individual) individual {
	//Randomly select number of genes to mutate, but at least 1
	numOfGenesToMutate := rand.Intn(maxMutatedGenes-1) + 1
	for i := 0; i < numOfGenesToMutate; i++ {
		//Generate random old position for the gene between 0 and one element before last
		oldPosition := rand.Intn(len(individual.tasks) - 1)
		//Generate random new position for the gene between oldPosition+1 and last element
		newPosition := rand.Intn(len(individual.tasks)-oldPosition-1) + oldPosition + 1
		//Store the original taskID at the oldPosition
		oldTaskID := individual.tasks[oldPosition].taskID
		//Shift all taskIDs one task back
		for j := range individual.tasks[oldPosition:newPosition] {
			individual.tasks[j].taskID = individual.tasks[j+1].taskID
		}
		//Restore the original taskID to the newPosition
		individual.tasks[newPosition].taskID = oldTaskID
	}
	return individual
}

func swapMutation(individual individual) individual {
	//Randomly select number of genes to mutate, but at least 1
	numOfGenesToMutate := rand.Intn(maxMutatedGenes-1) + 1
	sampleOrder := rand.Perm(len(individual.tasks))
	for i := 0; i < numOfGenesToMutate; i++ {
		//Swap taskIDs for the task with number sampleOrder[i] and sampleOrder[len(individual.tasks)-1] to make it easier to account for the border values
		individual.tasks[sampleOrder[i]].taskID, individual.tasks[sampleOrder[len(individual.tasks)-i-1]].taskID = individual.tasks[sampleOrder[len(individual.tasks)-i-1]].taskID, individual.tasks[sampleOrder[i]].taskID
	}
	return individual

}

func mutateIndividuals(individuals []individual) []individual {
	var mutatedIndividuals []individual
	//var crossoverStart, crossoverEnd, crossoverLen int
	//Copy parent to child individuals slice
	//mutatedIndividuals = make([]individual, len(individuals))
	mutatedIndividuals = copyPopulation(individuals)
	for i := range mutatedIndividuals {
		//Check if we need to mutate
		if rand.Float32() < mutationRate {
			if rand.Float32() < mutationTypePreference {
				//Do the displacement mutation
				mutatedIndividuals[i] = displacementMutation(mutatedIndividuals[i])
			} else {
				//Do the swap mutation
				mutatedIndividuals[i] = swapMutation(mutatedIndividuals[i])
			}
		}
	}
	return mutatedIndividuals
}

func crossoverIndividuals(parentIndividuals []individual) []individual {
	var childIndividuals []individual
	//var crossoverStart, crossoverEnd, crossoverLen int
	//Copy parent to child individuals slice
	//childIndividuals = make([]individual, len(parentIndividuals))
	childIndividuals = copyPopulation(parentIndividuals)
	//Check if we need to crossover
	if rand.Float32() < crossoverRate {
		crossoverStart := rand.Intn(len(childIndividuals[0].tasks))
		crossoverLen := rand.Intn(maxCrossoverLength)
		crossoverEnd := crossoverStart + crossoverLen
		if crossoverEnd > len(childIndividuals[0].tasks) {
			crossoverEnd = len(childIndividuals[0].tasks)
		}
		//TODO: Add random selection of the swappable individuals
		for i := range childIndividuals {
			//Swap part of the tasks slice between first and second individual
			for j := crossoverStart; j < crossoverEnd; j++ {
				first := i
				second := i + 1
				if second == len(childIndividuals) {
					second = 0
				}
				//Swap current task between first and second individual
				childIndividuals[first].tasks[j], childIndividuals[second].tasks[j] = childIndividuals[second].tasks[j], childIndividuals[first].tasks[j]
			}
		}
	}
	return childIndividuals
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
	logger.Debug("Infinite loop until no workers can be assigned")
	for condition := true; condition; condition = workerAssigned {
		//Prevent loops if no tasks left to process
		workerAssigned = false
		//Loop across all tasks
		for i, task := range individual.tasks {
			//logger.Debug("taskID =", task.taskID)
			//Process only tasks with remaining worker slots and with all the dependencies met
			if len(task.assignees) < tasksDB[task.taskID].idealWorkerCount && task.numPrerequisites == 0 {
				//Assign workers to the task until idealWorkerCount
				for j := len(individual.tasks[i].assignees); j < tasksDB[task.taskID].idealWorkerCount; j++ {
					//logger.Debug("worker j =", j)
					//Calculate fitness of idealWorkerCount workers for specific task
					//TODO: Add "taint" flag to worker to prevent recalculation of fitness for untouched workers
					calculateWorkersFitness(task, individual.workers)
					//logger.Debug(task)
					//Try to assign worker to task and update worker data
					//TODO: Multiple bool assignments. Any way to make it better?
					individual.tasks[i], workerAssigned = assignBestWorker(task, individual.workers)
					//logger.Debug(individual.tasks[i])
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
func prettyPrintTask(task scheduledTask) {
	name := tasksDB[task.taskID].name
	id := strings.Split(task.taskID, ".")[1]
	projectID := tasksDB[task.taskID].project
	projectName := projectsDB[tasksDB[task.taskID].project].name
	currentTime := time.Now()
	originDateTime := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day()+1, 8, 30, 0, 0, currentTime.Location())
	startWorkingMinutes := math.Floor(float64(task.startTime)/8)*1440 + math.Mod(float64(task.startTime), 8)*60
	stopWorkingMinutes := math.Floor(float64(task.stopTime)/8)*1440 + math.Mod(float64(task.stopTime), 8)*60
	startDateTime := originDateTime.Add(time.Duration(startWorkingMinutes) * time.Minute)
	stopDateTime := originDateTime.Add(time.Duration(stopWorkingMinutes) * time.Minute)
	workersIDs := strings.Join(task.assignees, ",")
	var predecessors []string
	var workers []string
	for _, v := range task.assignees {
		workers = append(workers, workersDB[v].name)
	}
	workersNames := strings.Join(workers, ",")
	for k := range tasksDB[task.taskID].prerequisites {
		predecessors = append(predecessors, k)
	}
	predecessorsIDs := strings.Join(predecessors, ",")

	logger.Infof(";%v;%v;%v;%v;%v;%v;%v;%v;%v", startDateTime.Format(("2006/01/02 15:04")), stopDateTime.Format(("2006/01/02 15:04")), projectName, name, workersNames, workersIDs, id, projectID, predecessorsIDs)
}

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
		logger.Info("Generation ", i)
		//Mutate and crossover population
		population = copyPopulation(transmogrifyPopulation(population))
		//Generate schedule and calculate fitness
		generatePopulationSchedules(population)
		//Sort population in the fitness order
		sortPopulation(population)
		logger.Info("Best fitness = ", population[0].fitness)

		/* 		for i, v := range population {
		   			if i < 10 {
		   				logger.Info(v.fitness)
		   			}
		   			loggerFile.Info(v)
		   			 			for _, task := range v.tasks {
		   			   				logger.Debug(task)
		   			   			}

				   }
		*/
	}
	logger.Info("Best schedule")
	for _, task := range population[0].tasks {
		prettyPrintTask(task)
	}
}
