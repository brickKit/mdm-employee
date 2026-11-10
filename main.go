// Command mdm-employee runs the employee component alone (serve, migrate); the shells compile in employee.Definition().
package main

import (
	"github.com/brickKit/be-sdk-go/v3/launcher"
	"github.com/brickKit/mdm-employee/v3/employee"
)

func main() { launcher.Main(employee.Definition()) }
