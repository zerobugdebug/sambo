package main

import (
	"encoding/csv"
	"hash/fnv"
	"io"
	"math"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"gitlab.com/alex.skylight/sambo/calendar"
	"gitlab.com/alex.skylight/sambo/go-log"
	"gitlab.com/alex.skylight/sambo/location"
)

const (
	workersDBFileName            string = "worker_info.csv"
	tasksDBFileName              string = "task_info.csv"
	projectsDBFileName           string = "project_info.csv"
	projectFamiliarityDBFileName string = "worker_project_hours.csv"
	workersTimeOffDBFileName     string = "worker_time_off.csv"
)

//Genetic algorithm parameters
var (
	populationSize         int     = 5     //size of the population
	generationsLimit       int     = 1     //how many generations to generate
	crossoverRate          float32 = 0.9   //how often to do crossover 0%-100% in decimal
	mutationRate           float32 = 0.9   //how often to do mutation 0%-100% in decimal
	elitismRate            float32 = 0.2   //how many of the best indviduals to keep intact
	deadend                float32 = 10000 //round number to split between unscheduled tasks and real hours to complete
	tourneySampleSize      int     = 3     //sample size for the tournament selection, should be less than population size-number of elites
	crossoverParentsNumber int     = 2     //number of parents for the crossover
	maxCrossoverLength     int     = 3     //max number of sequential tasks to cross between individuals
	maxMutatedGenes        int     = 3     //maximum number of mutated genes, min=2
	mutationTypePreference float32 = 0.5   //prefered mutation type rate. 0 = 100% swap mutation, 1 = 100% displacement mutation
)

//Worker best fit, weighted decision matrix (AHP)
const (
	weightDistance           float32 = 1
	weightDelay              float32 = 1
	weightProjectFamiliarity float32 = 0.1
	weightDemand             float32 = 0.5
	maxValueDriving          float32 = 4  //max driving time in hours
	maxValueDelay            float32 = 10 //~6 minutes delay
	maxValueDemand           float32 = 1  //worker can be assigned to all tasks
	pinnedDateTimeSnap       float32 = 8
	//weightTrades             float32 = 1 //for the trades implementation

)

//Additional constants
const (
	defaultDateFormat     string = "2006-01-02"       //format of date in the csv files
	defaultTimeFormat     string = "15:04"            //format of time in the csv files
	defaultDateTimeFormat string = "2006-01-02T15:04" //format of datetime in the csv files
	threadsNum            int    = 256                //number of go routines to run simultaneously
)

type dateTimeRange struct {
	startTime time.Time
	endTime   time.Time
}

type worker struct {
	name          string
	latitude      float64
	longitude     float64
	demand        float32 //how many tasks could potentialy be assigned to worker
	blockedRanges []dateTimeRange
}

type scheduledWorker struct {
	workerID                string
	availableAt             time.Time //earliest available time for the new task
	canStartTaskAt          time.Time //earliest time to start specific task, depends on duration, block time, etc
	blockedRanges           []dateTimeRange
	latitude                float64
	longitude               float64
	fitness                 float32
	valueDelay              float32
	valueDriving            float32
	valueProjectFamiliarity float32
	valueDemand             float32
	// valueTrades             float32
}

type project struct {
	name            string
	latitude        float64
	longitude       float64
	targetStartDate time.Time
	targetEndDate   time.Time
	site            calendar.Site
}

type individual struct {
	tasks       []scheduledTask
	workers     []scheduledWorker
	fitness     float32
	fitnessData struct {
		unscheduledTasks int
		finishDateTime   time.Time
	}
}

type population struct {
	individuals []individual
	hashes      map[uint64]int
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
	startTime        time.Time
	stopTime         time.Time
	assignees        []string
	numPrerequisites int
}

//Global variables to act as a in-memory reference DB
//TODO: Replace with some external in memory storage, because global vars are BAD
var tasksDB map[string]task                            //key is the task ID
var workersDB map[string]worker                        //key is the worker ID
var projectsDB map[string]project                      //key is the project ID
var projectFamiliarityDB map[string]map[string]float32 //key1 is the project ID, key2 is the worker ID

var scheduleStartTime time.Time
var logger = log.New(os.Stdout).WithoutDebug()

//.WithColor()

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
		projectTemp.site.DailyStartTime, err = time.Parse(defaultTimeFormat, projectsRecord[7])
		if err != nil {
			logger.Error("Original record: ", projectsRecord)
			logger.Fatal("Couldn't parse project daily start time value", err)
		}
		projectTemp.site.DailyEndTime, err = time.Parse(defaultTimeFormat, projectsRecord[8])
		if err != nil {
			logger.Error("Original record: ", projectsRecord)
			logger.Fatal("Couldn't parse project daily end time value", err)
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

		taskTemp.pinnedDateTime = time.Time{}
		if tasksRecord[10] != "" {
			logger.Debugf("PinnedDateTime:=%v", tasksRecord[10])
			taskTemp.pinnedDateTime, err = time.ParseInLocation(defaultDateTimeFormat, tasksRecord[10], scheduleStartTime.Location())
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

func verifyTaskDB() {
	//Verify all prerequisites
	for k, task := range tasksDB {
		if len(task.prerequisites) > 0 {
			logger.Debug("Verifying task:", k)
			for k := range task.prerequisites {
				logger.Debug("Verifying prereq:", k)
				if _, ok := tasksDB[k]; !ok {
					logger.Error("Original task: ", task)
					logger.Fatal("Prerequisite is missing: ", k)
				}
			}
		}
	}

	//TODO: Verify that predecessors are not circular
	//TODO: Verify that predecessors and successors are not pinned to the same DateTime
	//TODO: Verify that pinned worker is part of valid workers (?)

	//Verify double pinning
	for firstKey, firstTask := range tasksDB {
		//Both time and worker pinned
		if !firstTask.pinnedDateTime.IsZero() && len(firstTask.pinnedWorkerIDs) > 0 {
			for secondKey, secondTask := range tasksDB {
				if firstKey == secondKey {
					continue
				}
				if firstTask.pinnedDateTime.Equal(secondTask.pinnedDateTime) && reflect.DeepEqual(firstTask.pinnedWorkerIDs, secondTask.pinnedWorkerIDs) {
					//Both time and worker pinned in 2 tasks in the same time
					logger.Error("Double pinning encountered!")
					logger.Errorf("First Task ID:%v,Second Task ID:%v ", firstKey, secondKey)
				}
			}
		}
		if !firstTask.pinnedDateTime.IsZero() {
			logger.Debug("Daily start time=", projectsDB[firstTask.project].site.DailyStartTime)
			siteStartTime := time.Date(scheduleStartTime.Year(), scheduleStartTime.Month(), scheduleStartTime.Day(), projectsDB[firstTask.project].site.DailyStartTime.Hour(), projectsDB[firstTask.project].site.DailyStartTime.Minute(), projectsDB[firstTask.project].site.DailyStartTime.Second(), 0, scheduleStartTime.Location())
			//Check if pinned datetime is older than earliest possible datetime
			if firstTask.pinnedDateTime.Before(siteStartTime) {
				logger.Error("Task pinned in the past")
				logger.Errorf("Task ID:%v", firstKey)
			}
			//Check if pinned datetime is on the weekend
			if firstTask.pinnedDateTime.Weekday() == time.Saturday || firstTask.pinnedDateTime.Weekday() == time.Sunday {
				logger.Error("Task pinned on the weekend")
				logger.Errorf("Task ID:%v", firstKey)
			}
		}
	}

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

func readWorkerTimeOffCSV(workers map[string]worker) map[string]worker {
	var tempWorker worker
	var blockedRange dateTimeRange
	var hours float64
	workersTimeOffDBFile, err := os.Open(workersTimeOffDBFileName)
	if err != nil {
		logger.Fatal("Couldn't open the "+workersTimeOffDBFileName+" file\r\n", err)
	}
	workersTimeOffData := csv.NewReader(workersTimeOffDBFile)
	_, err = workersTimeOffData.Read() //skip CSV header
	for {
		workersTimeOffRecord, err := workersTimeOffData.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			logger.Fatal(err)
		}

		blockedRange.startTime, err = time.ParseInLocation(defaultDateTimeFormat, workersTimeOffRecord[0], scheduleStartTime.Location())
		if err != nil {
			logger.Error("Original record: ", workersTimeOffRecord)
			logger.Fatal("Couldn't parse datetime start value", err)
		}

		hours, err = strconv.ParseFloat(workersTimeOffRecord[1], 32)
		if err != nil {
			logger.Error("Original record: ", workersTimeOffRecord)
			logger.Fatal("Couldn't parse hours value", err)
		}
		blockedRange.endTime = blockedRange.startTime.Add(time.Duration(hours) * time.Hour)

		tempWorker = workers[workersTimeOffRecord[2]]
		tempWorker.blockedRanges = append(tempWorker.blockedRanges, blockedRange)
		logger.Debugf("WorkerID=%v, startTime=%v, endTime=%v", workersTimeOffRecord[2], blockedRange.startTime, blockedRange.endTime)
		workers[workersTimeOffRecord[2]] = tempWorker

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

//Calculate FNV-1a-64 hash to compare the order of the tasks between 2 individuals
func calcTasksHash(tasks []scheduledTask) uint64 {
	var allTasks []string
	//Gather all tasks into allTasks slice
	for _, v := range tasks {
		allTasks = append(allTasks, v.taskID)
	}
	//Convert slice into string representation
	allTasksString := strings.Join(allTasks, ",")
	logger.Debug("allTasksString=", allTasksString)
	//Calculate hash
	hashAlg := fnv.New64a()
	hashAlg.Write([]byte(allTasksString))
	return hashAlg.Sum64()
}

//Calculate hash for the individual
func calcIndividualHash(individual individual) uint64 {
	return calcTasksHash(individual.tasks)
}

//Calculate hash for the individuals
func calcIndividualsHash(individuals []individual) map[uint64]int {
	hashMap := make(map[uint64]int)
	for i, v := range individuals {
		hashMap[calcIndividualHash(v)] = i
	}
	return hashMap
}

//Generate individual by randomizing the taskDB
func generateIndividual() individual {
	var newIndividual individual
	taskOrder := rand.Perm(len(tasksDB))
	newIndividual.tasks = make([]scheduledTask, len(tasksDB))
	i := 0
	for k, v := range tasksDB {
		newIndividual.tasks[taskOrder[i]].taskID = k
		newIndividual.tasks[taskOrder[i]].startTime = time.Time{}
		newIndividual.tasks[taskOrder[i]].stopTime = time.Time{}
		newIndividual.tasks[taskOrder[i]].assignees = make([]string, 0)
		newIndividual.tasks[taskOrder[i]].numPrerequisites = len(v.prerequisites)
		i++
	}

	i = 0
	newIndividual.workers = make([]scheduledWorker, len(workersDB))
	for k, v := range workersDB {
		newIndividual.workers[i].workerID = k
		newIndividual.workers[i].availableAt = scheduleStartTime
		newIndividual.workers[i].latitude = v.latitude
		newIndividual.workers[i].longitude = v.longitude
		newIndividual.workers[i].fitness = 0
		newIndividual.workers[i].valueDelay = 0
		newIndividual.workers[i].valueDemand = 0
		newIndividual.workers[i].valueDriving = 0
		newIndividual.workers[i].valueProjectFamiliarity = 0
		i++
	}

	return newIndividual
}

//Reset individual state
func resetIndividual(individual individual) individual {
	for i, v := range individual.tasks {
		individual.tasks[i].startTime = time.Time{}
		individual.tasks[i].stopTime = time.Time{}
		individual.tasks[i].assignees = make([]string, 0)
		individual.tasks[i].numPrerequisites = len(tasksDB[v.taskID].prerequisites)
	}

	for i, v := range individual.workers {
		individual.workers[i].availableAt = scheduleStartTime
		individual.workers[i].latitude = workersDB[v.workerID].latitude
		individual.workers[i].longitude = workersDB[v.workerID].longitude
		individual.workers[i].fitness = 0
		individual.workers[i].valueDelay = 0
		individual.workers[i].valueDemand = 0
		individual.workers[i].valueDriving = 0
		individual.workers[i].valueProjectFamiliarity = 0
	}
	return individual
}

func generatePopulation() population {
	var population population
	for i := 0; i < populationSize; i++ {
		population.individuals = append(population.individuals, generateIndividual())
	}
	return population
}

//Calculate fitness for every worker for the current task
func calculateWorkersFitness(task scheduledTask, workers []scheduledWorker) {
	for i, v := range workers {

		//Caclulate earliest time to do the specific task for the current worker
		//for

		//Smaller wait time => higher number => better fit
		//valueDelay := v.availableAt.Sub
		var valueDelay float32
		if v.availableAt.Equal(scheduleStartTime) {
			valueDelay = maxValueDelay
		} else {
			valueDelay = float32(1 / v.availableAt.Sub(scheduleStartTime).Hours())
		}

		//More hours in project => higher number => better fit
		valueProjectFamiliarity := projectFamiliarityDB[tasksDB[task.taskID].project][v.workerID]

		//Shorter distance => higher number => better fit
		valueDriving := location.CalcDrivingTime(v.latitude, v.longitude, projectsDB[tasksDB[task.taskID].project].latitude, projectsDB[tasksDB[task.taskID].project].longitude)
		//logger.Debug(v.latitude, v.longitude, projectsDB[tasksDB[task.taskID].project].latitude, projectsDB[tasksDB[task.taskID].project].longitude)

		if valueDriving == 0 {
			valueDriving = maxValueDriving
		} else {
			valueDriving = 1 / valueDriving
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
		workers[i].valueDelay = valueDelay
		workers[i].valueProjectFamiliarity = valueProjectFamiliarity
		workers[i].valueDriving = valueDriving
		workers[i].valueDemand = valueDemand
		//v.valueTrades = valueTrades //TRADES IMPLEMENTATION

		if _, ok := tasksDB[task.taskID].pinnedWorkerIDs[v.workerID]; ok {
			workers[i].fitness = float32(math.MaxFloat32)
		}
		logger.Debug("Values=", workers[i].workerID, valueDelay, valueProjectFamiliarity, valueDriving, valueDemand)
		//Calculate AHP fitness for the worker, higher number => better fit
		workers[i].fitness = valueDelay*weightDelay + valueProjectFamiliarity*weightProjectFamiliarity + valueDriving*weightDistance + valueDemand*weightDemand
		logger.Debug("Normalized=", workers[i].workerID, valueDelay*weightDelay, valueProjectFamiliarity*weightProjectFamiliarity, valueDriving*weightDistance, valueDemand*weightDemand, workers[i].fitness)
		logger.Debugf("%v=%v", v.workerID, workers[i].fitness)
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
		//Skip the all other workers if pinnedWorker is not empty
		_, ok := tasksDB[task.taskID].pinnedWorkerIDs[worker.workerID]
		if len(tasksDB[task.taskID].pinnedWorkerIDs) > 0 && !ok {
			continue
		}
		//Assign only if worker can be assigned to this task
		//Check if workerID exists in the validWorkers map in taskDB
		if _, ok := tasksDB[task.taskID].validWorkers[worker.workerID]; ok {
			//Worker is a valid worker and can be potentially assigned
			logger.Debugf("Can be assigned, task:%v, worker:%v, start:%v", task.taskID, worker.workerID, worker.availableAt)

			//TODO: Ignore first driving time from home

			//Earliest possible task start time
			newStartTime := projectsDB[tasksDB[task.taskID].project].site.AddHours(worker.availableAt, float32(math.Round(100/float64(worker.valueDriving))/100))
			//Snapping range for the startTime
			newStartTimeWithSnap := projectsDB[tasksDB[task.taskID].project].site.AddHours(newStartTime, pinnedDateTimeSnap)
			newPinnedTimeWithSnap := projectsDB[tasksDB[task.taskID].project].site.AddHours(tasksDB[task.taskID].pinnedDateTime, pinnedDateTimeSnap)
			//If tasksDB[task.taskID].pinnedDateTime < newStartTime+pinnedDateTimeSnap < newPinnedTimeWithSnap+pinnedDateTimeSnap then task be snapped to the pinned datetime
			taskCanBeSnapped := newStartTimeWithSnap.After(tasksDB[task.taskID].pinnedDateTime) && newStartTimeWithSnap.Before(newPinnedTimeWithSnap)

			//Check if task is not pinned, or pinned and in the snap range
			if tasksDB[task.taskID].pinnedDateTime.IsZero() || (!tasksDB[task.taskID].pinnedDateTime.IsZero() && taskCanBeSnapped) {
				//Task can be assigned
				if tasksDB[task.taskID].pinnedDateTime.IsZero() {
					logger.Debugf("Task is not pinned. task.startTime=%v, newStartTime=%v", task.startTime, newStartTime)
					//Task is not pinned
					//startTime should be changed ONLY for never scheduled tasks (with predecessors or without them)
					if task.startTime.IsZero() {
						//Task was never scheduled and task has no predecessors
						task.startTime = newStartTime
					} else if task.stopTime.IsZero() && task.startTime.Before(newStartTime) {
						//Task was never scheduled, but start time defined by predecessors
						task.startTime = newStartTime
					}
				} else {
					//Task is pinned, so start time should be equal to pinned time
					logger.Debugf("Task pinned. pinnedDateTime=%v, newStartTimeWithSnap=%v, newPinnedTimeWithSnap=%v, newStartTime=%v", tasksDB[task.taskID].pinnedDateTime, newStartTimeWithSnap, newPinnedTimeWithSnap, newStartTime)
					task.startTime = tasksDB[task.taskID].pinnedDateTime
				}

				task.assignees = append(task.assignees, worker.workerID)

				//logger.Debug(task)
				newStopTime := projectsDB[tasksDB[task.taskID].project].site.AddHours(task.startTime, tasksDB[task.taskID].duration)
				//Extend stop time if current worker can't finish in time
				if task.stopTime.Before(newStopTime) {
					task.stopTime = newStopTime
				}
				//logger.Debug(task)
				//Change worker's next start time
				workers[i].availableAt = task.stopTime

				//Change worker's location
				workers[i].latitude = projectsDB[tasksDB[task.taskID].project].latitude
				workers[i].longitude = projectsDB[tasksDB[task.taskID].project].longitude

				//Assign success flag to prevent loops on the calling function
				workerAssigned = true
				//Worker assigned, ignore other workers
				break
			}

			//logger.Debugf("New start time:%v", newStartTime)

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
		valueDelay := v.availableAt
		if valueDelay == 0 {
			valueDelay = maxValueDelay
		} else {
			valueDelay = 1 / valueDelay
		}

		//More hours in project => higher number => better fit
		valueProjectFamiliarity := projectFamiliarityDB[tasksDB[task.taskID].project][v.workerID]

		//Shorter distance => higher number => better fit
		valueDriving := calcDistance(v.latitude, v.longitude, projectsDB[tasksDB[task.taskID].project].latitude, projectsDB[tasksDB[task.taskID].project].longitude)
		if valueDriving == 0 {
			valueDriving = maxvalueDriving
		} else {
			valueDriving = 1 / valueDriving
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

		v.valueDriving = valueDriving
		v.valueProjectFamiliarity = valueProjectFamiliarity
		//		v.valueTrades = valueTrades
		v.valueDelay = valueDelay
		//Calculate AHP fitness for the worker, higher number => better fit
		v.fitness = valueDelay*weightDelay + valueProjectFamiliarity*weightProjectFamiliarity + valueDriving*weightDistance // + valueTrades*weightTrades
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
			task.startTime = workers[0].availableAt + drivingSpeed/workers[i].valueDriving

			//Keep stop time intact for the multiple trades with different availability
			if task.stopTime-task.startTime < tasksDB[task.taskID].duration {
				task.stopTime = task.startTime + tasksDB[task.taskID].duration
			}
			//Change worker's next start time
			workers[i].availableAt = task.startTime + tasksDB[task.taskID].duration

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
	newIndividual.fitness = oldIndividual.fitness
	return newIndividual
}

func copyIndividuals(oldIndividuals []individual) []individual {
	var newIndividuals []individual
	for _, v := range oldIndividuals {
		newIndividuals = append(newIndividuals, copyIndividual(v))
	}
	return newIndividuals
}

//Apply crossovers and mutations on non-elite individuals
func transmogrifyPopulation(pop population) population {
	elitesNum := int(elitismRate * float32(len(pop.individuals)))
	//logger.Info("elitesNum=", elitesNum)
	var newPopulation population
	var tempIndividuals []individual
	//Keep elites in the new population
	//	newPopulation = population[:elitesNum]
	//logger.Info("OldElite=", population[0])
	newPopulation.individuals = copyIndividuals(pop.individuals[:elitesNum])
	//Recalculate hash for the elites
	newPopulation.hashes = calcIndividualsHash(newPopulation.individuals)
	//logger.Info("NewElite=", newPopulation[0])
	logger.Debug("newPopulation size with elites =", len(newPopulation.individuals))
	logger.Debug("Best elite fitness =", newPopulation.individuals[0].fitness)
	//loggerFile.Info("ELITES:", newPopulation[0].tasks)
	remainingIndividualsNumber := len(pop.individuals) - elitesNum
	logger.Debug("remainingIndividualsNumber =", remainingIndividualsNumber)
	//Generate len(population)-elitesNum additonal individuals
	for condition := true; condition; condition = remainingIndividualsNumber > 0 {
		tempIndividuals = make([]individual, crossoverParentsNumber)
		//Select crossoverParentsNumber from the population with Torunament Selection
		tempIndividuals = tourneySelect(pop.individuals, crossoverParentsNumber)
		logger.Debug("tempPopulation size after tourney =", len(tempIndividuals))
		//Apply crossover to the tempPopulation
		tempIndividuals = crossoverIndividualsOX1(tempIndividuals)
		logger.Debug("tempPopulation size after crossover =", len(tempIndividuals))
		//Apply mutation to the tempPopulation
		tempIndividuals = mutateIndividuals(tempIndividuals)
		logger.Debug("tempPopulation size after mutation =", len(tempIndividuals))
		//Append tempPopulation to the new population, if indviduals are new
		for _, v := range tempIndividuals {
			tempHash := calcIndividualHash(v)
			//If hash doesn't exist in the hashes map
			if _, ok := newPopulation.hashes[tempHash]; !ok {
				//Add hash with value of index of current individual
				newPopulation.hashes[tempHash] = len(newPopulation.individuals)
				//Add individual to the individuals slice
				newPopulation.individuals = append(newPopulation.individuals, copyIndividual(v))
				remainingIndividualsNumber--
			}
		}

		logger.Debug("newPopulation size =", len(newPopulation.individuals))
		//Update remaining number of individuals to generate
		logger.Debug("remainingIndividualsNumber =", remainingIndividualsNumber)
		logger.Debug("condition =", condition)
	}

	logger.Debug("newPopulation.hashes=", newPopulation.hashes)
	//Cut extra individuals generated by mutation/crossover
	newPopulation.individuals = newPopulation.individuals[:len(pop.individuals)]
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
	numOfGenesToMutate := rand.Intn(maxMutatedGenes) + 1
	for i := 0; i < numOfGenesToMutate; i++ {
		//Generate random old position for the gene between 0 and one element before last
		oldPosition := rand.Intn(len(individual.tasks) - 1)
		//Generate random new position for the gene between oldPosition+1 and last element
		newPosition := rand.Intn(len(individual.tasks)-oldPosition-1) + oldPosition + 1
		//Store the original taskID at the oldPosition
		oldTaskID := individual.tasks[oldPosition].taskID
		//Shift all taskIDs one task back
		for j := range individual.tasks[oldPosition:newPosition] {
			individual.tasks[oldPosition+j].taskID = individual.tasks[oldPosition+j+1].taskID
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
	mutatedIndividuals = copyIndividuals(individuals)
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

//Crossover indviduals by Order 1 method (OX1)
func crossoverIndividualsOX1(parentIndividuals []individual) []individual {
	//var childIndividuals []individual
	//var crossoverStart, crossoverEnd, crossoverLen int
	//Copy parent to child individuals slice
	childIndividuals := copyIndividuals(parentIndividuals)
	sizeIndividualTasks := len(childIndividuals[0].tasks)
	//Check if we need to crossover

	if rand.Float32() < crossoverRate {
		crossoverStart := rand.Intn(sizeIndividualTasks)
		crossoverLen := rand.Intn(maxCrossoverLength)
		crossoverEnd := crossoverStart + crossoverLen
		if crossoverEnd > sizeIndividualTasks {
			crossoverEnd = sizeIndividualTasks
		}
		logger.Debug("crossoverStart=", crossoverStart)
		logger.Debug("crossoverLen=", crossoverLen)
		logger.Debug("crossoverEnd=", crossoverEnd)
		//TODO: Add random selection of the swappable individuals
		for i, parent := range parentIndividuals {
			logger.Debug("parent=", parent)
			logger.Debug("i=", i)
			//Map to store copied genes
			copiedGenes := make(map[string]struct{})
			//Copy selected number of genes from first parent to child
			for j := crossoverStart; j < crossoverEnd; j++ {
				logger.Debug("TaskID=", parent.tasks[j].taskID)
				childIndividuals[i].tasks[j].taskID = parent.tasks[j].taskID
				copiedGenes[parent.tasks[j].taskID] = struct{}{}
			}

			childIndex := 0
			parentIndex := 0

			//Loop across the last parent and copy non-repeating genes (tasks)
			for childIndex < sizeIndividualTasks && parentIndex < sizeIndividualTasks {
				parentTask := parentIndividuals[len(parentIndividuals)-i-1].tasks[parentIndex]
				logger.Debugf("childIndex=%v, parentIndex=%v", childIndex, parentIndex)
				if childIndex >= crossoverStart && childIndex < crossoverEnd {
					childIndex++
					continue
				}
				if _, ok := copiedGenes[parentTask.taskID]; !ok {
					childIndividuals[i].tasks[childIndex].taskID = parentTask.taskID
					childIndex++
				}
				parentIndex++

			}
		}
	}
	return childIndividuals
}

func crossoverIndividuals(parentIndividuals []individual) []individual {
	var childIndividuals []individual
	//var crossoverStart, crossoverEnd, crossoverLen int
	//Copy parent to child individuals slice
	//childIndividuals = make([]individual, len(parentIndividuals))
	childIndividuals = copyIndividuals(parentIndividuals)
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
	//Number of elites
	elitesNum := int(elitismRate * float32(len(population)))

	chanIndividualIn := make(chan individual)
	chanIndividualOut := make(chan individual)
	//Start go subroutines to handle the calculation
	for i := 0; i < threadsNum; i++ {
		go generateIndividualSchedule(chanIndividualIn, chanIndividualOut)
	}

	//Recalculate elites if they are not calculated
	if population[0].fitness == 0 {
		for i := range population[:elitesNum] {
			//logger.Info("Generating N=", i)\
			chanIndividualIn <- population[i]
			population[i] = <-chanIndividualOut
		}
	}

	//Recalculate everyone else
	j := elitesNum
	remainingThreads := 0
	for j < populationSize-1 {
		remainingThreads = populationSize - j - 1
		if remainingThreads > threadsNum {
			remainingThreads = threadsNum
		}
		for i := 0; i < remainingThreads; i++ {
			//Push data to the subroutines
			//logger.Info("Pushing data to subroutines")
			//logger.Info("j+i=", j+i)
			chanIndividualIn <- population[j+i]
			//logger.Info("Pushed data to subroutines")
		}
		for i := 0; i < remainingThreads; i++ {
			//logger.Info("Waiting for results ")
			population[j+i] = <-chanIndividualOut
			//logger.Info("Got result: ", population[j].fitness)
		}
		j += remainingThreads
		logger.Infof("%v individuals completed", j+1)

	}
	close(chanIndividualIn)
	close(chanIndividualOut)
}

//Generate individual schedule and calculate fitness subroutine
func generateIndividualSchedule(chanIndividualIn, chanIndividualOut chan individual) {
	//logger.Info("Subroutine started")
	for {
		individual, ok := <-chanIndividualIn
		//logger.Info("Got individual: ", individual.fitness)
		if ok == false {
			//logger.Info("Subroutine stopped")
			break
		}
		individual = resetIndividual(individual)
		var workerAssigned bool = true
		//Infinite loop until no workers can be assigned
		logger.Debug("Infinite loop until no workers can be assigned")
		for condition := true; condition; condition = workerAssigned {
			//Prevent loops if no tasks left to process
			workerAssigned = false
			//Loop across all tasks
			for i, task := range individual.tasks {
				logger.Debug("Processing taskID =", task.taskID)
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
									newStopTime := projectsDB[tasksDB[task.taskID].project].site.AddHours(prerequisiteTask.stopTime, tasksDB[task.taskID].prerequisites[prerequisiteTask.taskID])
									if individual.tasks[i].startTime.Before(newStopTime) {
										individual.tasks[i].startTime = newStopTime
									}

								}

							}

						}
					}
				}
			}
		}

		//Default to best individual
		individual.fitness = 0
		var unscheduledTasksNumber float32 = 0
		for _, task := range individual.tasks {
			//If we have tasks/trades with no workers assigned, the individual is a dead end
			if len(task.assignees) != tasksDB[task.taskID].idealWorkerCount {
				//Individual has unscheduled tasks. Fewer unscheduled tasks => better individual fitness
				logger.Debug("Can't schedule: ", task)
				unscheduledTasksNumber++
			}
			//Earlier stopTime => faster we finish all the tasks => better individual fitness
			if individual.fitness < float32(task.stopTime.Sub(scheduleStartTime).Hours()) {
				individual.fitness = float32(task.stopTime.Sub(scheduleStartTime).Hours())
			}
		}
		if unscheduledTasksNumber > 0 {
			individual.fitness = unscheduledTasksNumber*deadend + individual.fitness
		}
		//logger.Info("Sending individual: ", individual.fitness)
		chanIndividualOut <- individual
		//logger.Info("Individual sent: ", individual.fitness)
	}
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
	//currentTime := time.Now()
	//originDateTime := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day()+1, 8, 30, 0, 0, currentTime.Location())
	//startWorkingMinutes := math.Floor(float64(task.startTime)/8)*1440 + math.Mod(float64(task.startTime), 8)*60
	//stopWorkingMinutes := math.Floor(float64(task.stopTime)/8)*1440 + math.Mod(float64(task.stopTime), 8)*60
	startDateTime := task.startTime
	stopDateTime := task.stopTime
	workersIDs := strings.Join(task.assignees, ",")
	var predecessors, workers, pinnedWorkers []string
	var pinnedDateTime string
	for _, v := range task.assignees {
		workers = append(workers, workersDB[v].name)
	}
	workersNames := strings.Join(workers, ",")
	for k := range tasksDB[task.taskID].prerequisites {
		predecessors = append(predecessors, k)
	}
	predecessorsIDs := strings.Join(predecessors, ",")
	for k := range tasksDB[task.taskID].pinnedWorkerIDs {
		pinnedWorkers = append(pinnedWorkers, workersDB[k].name)
	}
	pinnedWorkersNames := strings.Join(pinnedWorkers, ",")
	if !tasksDB[task.taskID].pinnedDateTime.IsZero() {
		pinnedDateTime = tasksDB[task.taskID].pinnedDateTime.Format("2006/01/02 15:04")
	}

	logger.Infof(";%v;%v;%v;%v;%v;%v;%v;%v;%v;%v;%v", startDateTime.Format(("2006/01/02 15:04")), stopDateTime.Format(("2006/01/02 15:04")), projectName, name, workersNames, workersIDs, id, projectID, predecessorsIDs, pinnedWorkersNames, pinnedDateTime)
}

func main() {

	logger.Info("================================================")
	logger.Info("Current GA settings:")
	logger.Info("populationSize=", populationSize)
	logger.Info("generationsLimit=", generationsLimit)
	logger.Info("crossoverRate=", crossoverRate)
	logger.Info("mutationRate=", mutationRate)
	logger.Info("elitismRate=", elitismRate)
	logger.Info("deadend=", deadend)
	logger.Info("tourneySampleSize=", tourneySampleSize)
	logger.Info("crossoverParentsNumber=", crossoverParentsNumber)
	logger.Info("maxCrossoverLength=", maxCrossoverLength)
	logger.Info("maxMutatedGenes=", maxMutatedGenes)
	logger.Info("mutationTypePreference=", mutationTypePreference)
	logger.Info("================================================")
	logger.Info("Current workers AHP settings:")
	logger.Info("weightDistance=", weightDistance)
	logger.Info("weightDelay=", weightDelay)
	logger.Info("weightProjectFamiliarity=", weightProjectFamiliarity)
	logger.Info("weightDemand=", weightDemand)
	logger.Info("maxValueDriving=", maxValueDriving)
	logger.Info("maxValueDelay=", maxValueDelay)
	logger.Info("maxValueDemand=", maxValueDemand)
	logger.Info("pinnedDateTimeSnap=", pinnedDateTimeSnap)
	logger.Info("================================================")

	var population population
	rand.Seed(time.Now().UnixNano())

	currentTime := time.Now()
	scheduleStartTime = time.Date(2020, 12, 18, 0, 0, 0, 0, currentTime.Location())

	//projectsDB = make(map[string]project)
	//projectsDB, projectFamiliarityDB, tasksDB, workersDB, workersTimeOffDB = readCSVs()

	//Global DB vars can be accessed directly, but to follow the standard approach used as a func output
	projectsDB = readProjectInfoCSV()
	tasksDB = readTaskInfoCSV()
	workersDB = readWorkerInfoCSV()
	projectFamiliarityDB = readWorkerProjectHoursCSV()
	workersDB = readWorkerTimeOffCSV(workersDB)

	verifyTaskDB()

	workersDB = calculateWorkersDemand() //not neeeded if trades would be implemented
	//projectsDB = readProjectInfoCSV()
	//fmt.Println(projectsDB)
	//fmt.Println(tasksDB)
	//fmt.Println(workersDB)
	//fmt.Println(projectFamiliarityDB)
	population = generatePopulation()

	var stagnantGenerationsNumber int
	var stagnantGenerationsFitness float32
	for i := 0; i < generationsLimit; i++ {
		logger.Info("Generation", i)
		//Mutate and crossover population
		logger.Info("Mutating population...")
		population = transmogrifyPopulation(population)
		//population = transmogrifyPopulation(population)
		//Generate schedule and calculate fitness
		logger.Info("Generating schedules...")
		generatePopulationSchedules(population.individuals)
		logger.Info("Sorting individuals...")
		//Sort population in the fitness order
		sortPopulation(population.individuals)
		logger.Info("Best fitness =", population.individuals[0].fitness)
		logger.Info("Second best fitness =", population.individuals[1].fitness)
		logger.Info("Third best fitness =", population.individuals[2].fitness)

		logger.Info("Stagnant generations number =", stagnantGenerationsNumber)
		//Update number of stagnant generations
		if population.individuals[0].fitness+population.individuals[1].fitness+population.individuals[2].fitness != stagnantGenerationsFitness {
			stagnantGenerationsFitness = population.individuals[0].fitness + population.individuals[1].fitness + population.individuals[2].fitness
			stagnantGenerationsNumber = 0
		} else {
			stagnantGenerationsNumber++
		}
		//Add randomness to break the stagnation
		if stagnantGenerationsNumber > 50 {
			tourneySampleSize = rand.Intn(91) + 10
			crossoverParentsNumber = rand.Intn(3) + 2
			maxCrossoverLength = rand.Intn(91) + 10
			maxMutatedGenes = rand.Intn(91) + 10
			mutationTypePreference = rand.Float32()
			stagnantGenerationsNumber = 0
			logger.Info("================================================")
			logger.Info("Current GA settings:")
			logger.Info("populationSize=", populationSize)
			logger.Info("generationsLimit=", generationsLimit)
			logger.Info("crossoverRate=", crossoverRate)
			logger.Info("mutationRate=", mutationRate)
			logger.Info("elitismRate=", elitismRate)
			logger.Info("deadend=", deadend)
			logger.Info("tourneySampleSize=", tourneySampleSize)
			logger.Info("crossoverParentsNumber=", crossoverParentsNumber)
			logger.Info("maxCrossoverLength=", maxCrossoverLength)
			logger.Info("maxMutatedGenes=", maxMutatedGenes)
			logger.Info("mutationTypePreference=", mutationTypePreference)
			logger.Info("================================================")
		}

	}
	logger.Info("Best schedule")
	for _, task := range population.individuals[0].tasks {
		prettyPrintTask(task)
	}
}
