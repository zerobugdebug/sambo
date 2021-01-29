package main

import (
	"fmt"
	"math/rand"
	"sort"
	"time"
)

const (
	populationSize   int     = 5
	generationsLimit int     = 100
	crossoverRate    float32 = 1
	elitismRate      float32 = 0.05
	mutationRate     float32 = 0.25
)

var allowedTrades = [...]string{"Painter", "Lead", "Helper"}

type worker struct {
	ID    int
	name  string
	trade []string
}
type project struct{}
type individual struct {
	taskList []task
	fitness  int
}
type task struct {
	ID        int
	name      string
	startTime int
	assignee  int
	project   string
}

var population []individual
var taskList []task

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

func readCSVs() []task {
	taskList := []task{{1, "abc", 0, 0, ""}, {2, "def", 0, 0, ""}, {3, "ghi", 0, 0, ""}}
	//	taskList[0] = task{1, "abc", 0, 0, ""}
	//	taskList[1] = task{2, "def", 0, 0, ""}
	//	taskList[2] = task{3, "ghi", 0, 0, ""}
	readProjectInfoCSV()
	readTaskInfoCSV()
	readWorkerInfoCSV()
	readWorkerProjectHoursCSV()
	readWorkerTimeOffCSV()
	return taskList

}

func generateIndividual(taskList []task) individual {
	var newIndividual individual
	newIndividual.taskList = make([]task, len(taskList))
	result := copy(newIndividual.taskList, taskList)
	fmt.Println(result)
	//newIndividual.taskList[0].print()
	//newIndividual.taskList[1].print()
	//newIndividual.taskList[2].print()

	rand.Shuffle(len(newIndividual.taskList), func(i, j int) {
		newIndividual.taskList[i], newIndividual.taskList[j] = newIndividual.taskList[j], newIndividual.taskList[i]
	})

	return newIndividual
}

func generatePopulation(taskList []task) []individual {
	var population []individual
	for i := 0; i < populationSize; i++ {
		individual := generateIndividual(taskList)
		population = append(population, individual)
	}
	return population
}

func findBestWorker() {

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

func calculateFitness() {

}

func sortPopulation() {
	sort.Slice(population, func(i, j int) bool {
		return population[i].fitness < population[j].fitness
	})
}

func generateSchedules() {

}

func main() {
	rand.Seed(time.Now().UnixNano())
	taskList = readCSVs()
	taskList[0].print()
	taskList[1].print()
	taskList[2].print()
	population = generatePopulation(taskList)
	population[0].print()
	population[1].print()
	population[2].print()
	for i := 0; i < generationsLimit; i++ {
		generateSchedules()
		calculateFitness()
		sortPopulation()
		transmogrifyPopulation()
	}
}
