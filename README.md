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

`mesosdef -dryRun -file example.hcl` will compute the dependency graph for the
defined deployments and print them in the order they would be deployed

`mesosdef -file example.hcl` will simulate a deployment, with a chance of
failure for each resource, and print the results as they occur

To use the `example.hcl` in this repository, it is currently also necessary to
set the variables `deploy_root` and `dns_tld` which can be done with `-var`
arguments or environment variables; a working command line might be

```
mesosdef -file example.hcl -var dns_tld=mesos -var deploy_root=./deploy
```

### Future

`mesosdef validate` will statically validate files for syntatical correctness

`mesosdef plan` will compute a deployment plan without making changes

`mesosdef apply` will execute a deployment plan

`mesosdef destroy` will reverse a deployment plan
