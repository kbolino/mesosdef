# mesosdef

The goal of this project is to provide a declarative language for defining
Mesos framework deployments and their dependencies.

Support goals:
- Mesos 1.7+
- Marathon 1.5+
- Chronos 3.0

This project is modeled on Hashicorp terraform, taking the concept of
infrastructure-as-code and applying it to the Mesos ecosystem.

## Command line

### Currently

`mesosdef -file example.hcl` will compute the dependency graph for the defined
deployments and print them in the order they would be deployed

### Future

`mesosdef validate` will statically validate files for syntatical correctness

`mesosdef plan` will compute a deployment plan without making changes

`mesosdef apply` will execute a deployment plan

`mesosdef destroy` will reverse a deployment plan
