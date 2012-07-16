package service

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"github.com/timeredbull/tsuru/api/app"
	"github.com/timeredbull/tsuru/api/auth"
	"github.com/timeredbull/tsuru/api/unit"
	"github.com/timeredbull/tsuru/db"
	"github.com/timeredbull/tsuru/errors"
	"io/ioutil"
	"labix.org/v2/mgo/bson"
	. "launchpad.net/gocheck"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"time"
)

func makeRequestToCreateHandler(c *C) (*httptest.ResponseRecorder, *http.Request) {
	manifest := `id: some_service
endpoint:
    production: someservice.com
    test: test.someservice.com
`
	b := bytes.NewBufferString(manifest)
	request, err := http.NewRequest("POST", "/services", b)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	return recorder, request
}

func makeRequestToCreateInstanceHandler(c *C) (*httptest.ResponseRecorder, *http.Request) {
	b := bytes.NewBufferString(`{"name": "brainSQL", "service_name": "mysql", "app": "my_app"}`)
	request, err := http.NewRequest("POST", "/services/instances", b)
	c.Assert(err, IsNil)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	return recorder, request
}

func (s *S) TestCreateHandlerSavesNameFromManifestId(c *C) {
	recorder, request := makeRequestToCreateHandler(c)
	err := CreateHandler(recorder, request, s.user)
	c.Assert(err, IsNil)
	query := bson.M{"_id": "some_service"}
	var rService Service
	err = db.Session.Services().Find(query).One(&rService)
	c.Assert(err, IsNil)
	c.Assert(rService.Name, Equals, "some_service")
}

func (s *S) TestCreateHandlerSavesEndpointServiceProperty(c *C) {
	recorder, request := makeRequestToCreateHandler(c)
	err := CreateHandler(recorder, request, s.user)
	c.Assert(err, IsNil)
	query := bson.M{"_id": "some_service"}
	var rService Service
	err = db.Session.Services().Find(query).One(&rService)
	c.Assert(err, IsNil)
	c.Assert(rService.Endpoint["production"], Equals, "someservice.com")
	c.Assert(rService.Endpoint["test"], Equals, "test.someservice.com")
}

func (s *S) TestCreateHandlerWithContentOfRealYaml(c *C) {
	p, err := filepath.Abs("testdata/manifest.yml")
	manifest, err := ioutil.ReadFile(p)
	c.Assert(err, IsNil)
	b := bytes.NewBuffer(manifest)
	request, err := http.NewRequest("POST", "/services", b)
	c.Assert(err, IsNil)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	err = CreateHandler(recorder, request, s.user)
	c.Assert(err, IsNil)
	query := bson.M{"_id": "mysqlapi"}
	var rService Service
	err = db.Session.Services().Find(query).One(&rService)
	c.Assert(err, IsNil)
	c.Assert(rService.Endpoint["production"], Equals, "mysqlapi.com")
	c.Assert(rService.Endpoint["test"], Equals, "localhost:8000")
	c.Assert(rService.Bootstrap["ami"], Equals, "ami-00000007")
	c.Assert(rService.Bootstrap["when"], Equals, "on-new-instance")
}

func (s *S) TestCreateHandlerShouldReturnErrorWhenNameExists(c *C) {
	recorder, request := makeRequestToCreateHandler(c)
	err := CreateHandler(recorder, request, s.user)
	c.Assert(err, IsNil)
	recorder, request = makeRequestToCreateHandler(c)
	err = CreateHandler(recorder, request, s.user)
	c.Assert(err, Not(IsNil))
	c.Assert(err, ErrorMatches, "^Service with name some_service already exists.$")
}

func (s *S) TestCreateHandlerGetAllTeamsFromTheUser(c *C) {
	recorder, request := makeRequestToCreateHandler(c)
	err := CreateHandler(recorder, request, s.user)
	c.Assert(err, IsNil)
	c.Assert(recorder.Body.String(), Equals, "success")
	c.Assert(recorder.Code, Equals, 200)
	query := bson.M{"_id": "some_service"}
	var rService Service
	err = db.Session.Services().Find(query).One(&rService)
	c.Assert(err, IsNil)
	c.Assert(rService.Name, Equals, "some_service")
	c.Assert(*s.team, HasAccessTo, rService)
}

func (s *S) TestCreateHandlerReturnsForbiddenIfTheUserIsNotMemberOfAnyTeam(c *C) {
	u := &auth.User{Email: "enforce@queensryche.com", Password: "123"}
	u.Create()
	defer db.Session.Users().RemoveAll(bson.M{"email": u.Email})
	recorder, request := makeRequestToCreateHandler(c)
	err := CreateHandler(recorder, request, u)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusForbidden)
	c.Assert(e, ErrorMatches, "^In order to create a service, you should be member of at least one team$")
}

func (s *S) TestCreateInstanceHandlerVMOnNewInstanceWhenManifestSaysSo(c *C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"DATABASE_HOST":"localhost"}`))
	}))
	defer ts.Close()
	service := Service{
		Name: "mysql",
		Bootstrap: map[string]string{
			"ami":  "ami-0000007",
			"when": OnNewInstance,
		},
		Teams: []string{s.team.Name},
		Endpoint: map[string]string{
			"production": ts.URL,
		},
	}
	err := service.Create()
	c.Assert(err, IsNil)
	recorder, request := makeRequestToCreateInstanceHandler(c)
	err = CreateInstanceHandler(recorder, request, s.user)
	c.Assert(err, IsNil)
	q := bson.M{"_id": "brainSQL", "instances": bson.M{"$ne": ""}}
	var si ServiceInstance
	err = db.Session.ServiceInstances().Find(q).One(&si)
	c.Assert(err, IsNil)
	si.Host = "192.168.0.110"
	si.State = "running"
	db.Session.ServiceInstances().Update(bson.M{"_id": si.Name}, si)
	err = db.Session.ServiceInstances().Find(q).One(&si)
	c.Assert(err, IsNil)
	c.Assert(si.Instance, Equals, "i-0")
}

func (suite *S) TestCreateInstanceHandlerSavesServiceInstanceInDb(c *C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"DATABASE_HOST":"localhost"}`))
	}))
	defer ts.Close()
	s := Service{Name: "mysql", Teams: []string{suite.team.Name}, Endpoint: map[string]string{"production": ts.URL}}
	s.Create()
	defer s.Delete()
	recorder, request := makeRequestToCreateInstanceHandler(c)
	err := CreateInstanceHandler(recorder, request, suite.user)
	c.Assert(err, IsNil)
	var si ServiceInstance
	err = db.Session.ServiceInstances().Find(bson.M{"_id": "brainSQL", "service_name": "mysql"}).One(&si)
	c.Assert(err, IsNil)
	si.Host = "192.168.0.110"
	si.State = "running"
	db.Session.ServiceInstances().Update(bson.M{"_id": si.Name}, si)
	c.Assert(si.Name, Equals, "brainSQL")
	c.Assert(si.ServiceName, Equals, "mysql")
}

func (s *S) TestCreateInstanceHandlerCallsTheServiceAPIAndSaveEnvironmentVariablesInTheInstance(c *C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"DATABASE_HOST":"localhost"}`))
	}))
	defer ts.Close()
	service := Service{Name: "mysql", Teams: []string{s.team.Name}, Endpoint: map[string]string{"production": ts.URL}}
	service.Create()
	recorder, request := makeRequestToCreateInstanceHandler(c)
	err := CreateInstanceHandler(recorder, request, s.user)
	c.Assert(err, IsNil)
	var si ServiceInstance
	db.Session.ServiceInstances().Find(bson.M{"_id": "brainSQL"}).One(&si)
	si.Host = "192.168.0.110"
	si.State = "running"
	db.Session.ServiceInstances().Update(bson.M{"_id": si.Name}, si)
	time.Sleep(2e9)
	db.Session.ServiceInstances().Find(bson.M{"_id": "brainSQL"}).One(&si)
	c.Assert(si.Env, DeepEquals, map[string]string{"DATABASE_HOST": "localhost"})
}

func (s *S) TestCreateInstanceHandlerSavesAllTeamsThatTheGivenUserIsMemberAndHasAccessToTheServiceInTheInstance(c *C) {
	t := auth.Team{Name: "judaspriest", Users: []auth.User{*s.user}}
	err := db.Session.Teams().Insert(t)
	defer db.Session.Teams().Remove(bson.M{"name": t.Name})
	service := Service{Name: "mysql", Teams: []string{s.team.Name}}
	err = service.Create()
	c.Assert(err, IsNil)
	recorder, request := makeRequestToCreateInstanceHandler(c)
	err = CreateInstanceHandler(recorder, request, s.user)
	c.Assert(err, IsNil)
	var si ServiceInstance
	err = db.Session.ServiceInstances().Find(bson.M{"_id": "brainSQL"}).One(&si)
	c.Assert(err, IsNil)
	si.Host = "192.168.0.110"
	si.State = "running"
	db.Session.ServiceInstances().Update(bson.M{"_id": si.Name}, si)
	c.Assert(si.Teams, DeepEquals, []string{s.team.Name})
}

func (s *S) TestCreateInstanceHandlerDoesNotFailIfTheServiceDoesNotDeclareEndpoint(c *C) {
	service := Service{Name: "mysql", Teams: []string{s.team.Name}}
	service.Create()
	recorder, request := makeRequestToCreateInstanceHandler(c)
	err := CreateInstanceHandler(recorder, request, s.user)
	c.Assert(err, IsNil)
	var si ServiceInstance
	err = db.Session.ServiceInstances().Find(bson.M{"_id": "brainSQL"}).One(&si)
	c.Assert(err, IsNil)
	err = db.Session.ServiceInstances().Find(bson.M{"_id": "brainSQL"}).One(&si)
	c.Assert(err, IsNil)
	si.Host = "192.168.0.110"
	si.State = "running"
	db.Session.ServiceInstances().Update(bson.M{"_id": si.Name}, si)
}

func (s *S) TestCreateInstanceHandlerReturnsErrorWhenUserCannotUseService(c *C) {
	service := Service{Name: "mysql"}
	service.Create()
	recorder, request := makeRequestToCreateInstanceHandler(c)
	err := CreateInstanceHandler(recorder, request, s.user)
	c.Assert(err, ErrorMatches, "^You don't have access to service mysql$")
}

func (s *S) TestCreateInstanceHandlerReturnsErrorWhenServiceDoesntExists(c *C) {
	recorder, request := makeRequestToCreateInstanceHandler(c)
	err := CreateInstanceHandler(recorder, request, s.user)
	c.Assert(err, ErrorMatches, "^Service mysql does not exists.$")
}

func (s *S) TestCallServiceApi(c *C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"DATABASE_USER":"root","DATABASE_PASSWORD":"s3cr3t"}`))
	}))
	defer ts.Close()
	service := Service{Name: "mysql", Endpoint: map[string]string{"production": ts.URL}}
	err := service.Create()
	si := ServiceInstance{Name: "brainSQL", Host: "192.168.0.110", State: "running"}
	si.Create()
	defer si.Delete()
	c.Assert(err, IsNil)
	defer db.Session.Services().Remove(bson.M{"_id": "mysql"})
	callServiceApi(service, si)
	db.Session.ServiceInstances().Find(bson.M{"_id": si.Name}).One(&si)
	c.Assert(si.Env, DeepEquals, map[string]string{"DATABASE_USER": "root", "DATABASE_PASSWORD": "s3cr3t"})

}

func (s *S) TestAsyncCAllServiceApi(c *C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"DATABASE_USER":"root","DATABASE_PASSWORD":"s3cr3t"}`))
	}))
	defer ts.Close()
	service := Service{Name: "mysql", Endpoint: map[string]string{"production": ts.URL}}
	err := service.Create()
	si := ServiceInstance{Name: "brainSQL"}
	si.Create()
	defer si.Delete()
	go callServiceApi(service, si)
	c.Assert(err, IsNil)
	defer db.Session.Services().Remove(bson.M{"_id": "mysql"})
	si.State = "running"
	si.Host = "192.168.0.110"
	err = db.Session.ServiceInstances().Update(bson.M{"_id": si.Name}, si)
	c.Assert(err, IsNil)
	time.Sleep(2e9)
	db.Session.ServiceInstances().Find(bson.M{"_id": si.Name}).One(&si)
	c.Assert(si.Env, DeepEquals, map[string]string{"DATABASE_USER": "root", "DATABASE_PASSWORD": "s3cr3t"})
}

func (s *S) TestDeleteHandler(c *C) {
	se := Service{Name: "Mysql", Teams: []string{s.team.Name}}
	se.Create()
	defer se.Delete()
	request, err := http.NewRequest("DELETE", fmt.Sprintf("/services/%s?:name=%s", se.Name, se.Name), nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = DeleteHandler(recorder, request, s.user)
	c.Assert(err, IsNil)
	c.Assert(recorder.Code, Equals, 200)
	query := bson.M{"_id": "Mysql"}
	qtd, err := db.Session.Services().Find(query).Count()
	c.Assert(err, IsNil)
	c.Assert(qtd, Equals, 0)
}

func (s *S) TestDeleteHandlerReturns404(c *C) {
	request, err := http.NewRequest("DELETE", fmt.Sprintf("/services/%s?:name=%s", "mongodb", "mongodb"), nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = DeleteHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusNotFound)
	c.Assert(e, ErrorMatches, "^Service not found$")
}

func (s *S) TestDeleteHandlerReturns403IfTheUserDoesNotHaveAccessToTheService(c *C) {
	se := Service{Name: "Mysql"}
	se.Create()
	defer se.Delete()
	request, err := http.NewRequest("DELETE", fmt.Sprintf("/services/%s?:name=%s", se.Name, se.Name), nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = DeleteHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusForbidden)
	c.Assert(e, ErrorMatches, "^This user does not have access to this service$")
}

func (s *S) TestBindHandlerAddsTheAppToTheInstance(c *C) {
	service := Service{Name: "mysql"}
	err := service.Create()
	c.Assert(err, IsNil)
	defer db.Session.Services().Remove(bson.M{"_id": "mysql"})
	instance := ServiceInstance{Name: "my-mysql", ServiceName: "mysql", Teams: []string{s.team.Name}, State: "running"}
	err = instance.Create()
	c.Assert(err, IsNil)
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "my-mysql"})
	a := app.App{Name: "painkiller", Teams: []string{s.team.Name}}
	err = a.Create()
	c.Assert(err, IsNil)
	defer a.Destroy()
	url := fmt.Sprintf("/services/instances/%s/%s?:instance=%s&:app=%s", instance.Name, a.Name, instance.Name, a.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = BindHandler(recorder, request, s.user)
	c.Assert(err, IsNil)
	err = db.Session.ServiceInstances().Find(bson.M{"_id": instance.Name}).One(&instance)
	c.Assert(err, IsNil)
	c.Assert(instance.Apps, DeepEquals, []string{a.Name})
}

func (s *S) TestBindHandlerAddsAllEnvironmentVariableFromInstanceInTheAppAsPrivateVariables(c *C) {
	service := Service{Name: "mysql"}
	err := service.Create()
	c.Assert(err, IsNil)
	defer db.Session.Services().Remove(bson.M{"_id": "mysql"})
	instance := ServiceInstance{
		Name:        "my-mysql",
		ServiceName: "mysql",
		Teams:       []string{s.team.Name},
		Env:         map[string]string{"DATABASE_NAME": "mymysql", "DATABASE_HOST": "localhost"},
		State:       "running",
	}
	err = instance.Create()
	c.Assert(err, IsNil)
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "my-mysql"})
	a := app.App{Name: "painkiller", Teams: []string{s.team.Name}}
	err = a.Create()
	c.Assert(err, IsNil)
	defer a.Destroy()
	url := fmt.Sprintf("/services/instances/%s/%s?:instance=%s&:app=%s", instance.Name, a.Name, instance.Name, a.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = BindHandler(recorder, request, s.user)
	c.Assert(err, IsNil)
	err = db.Session.Apps().Find(bson.M{"name": a.Name}).One(&a)
	c.Assert(err, IsNil)
	expectedEnv := map[string]app.EnvVar{
		"DATABASE_NAME": app.EnvVar{
			Name:         "DATABASE_NAME",
			Value:        "mymysql",
			Public:       false,
			InstanceName: instance.Name,
		},
		"DATABASE_HOST": app.EnvVar{
			Name:         "DATABASE_HOST",
			Value:        "localhost",
			Public:       false,
			InstanceName: instance.Name,
		},
	}
	c.Assert(a.Env, DeepEquals, expectedEnv)
}

func (s *S) TestBindHandlerCallTheServiceAPIAndSetsEnvironmentVariablesReturnedInTheCallToTheApp(c *C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"DATABASE_USER":"root","DATABASE_PASSWORD":"s3cr3t"}`))
	}))
	defer ts.Close()
	service := Service{Name: "mysql", Endpoint: map[string]string{"production": ts.URL}}
	err := service.Create()
	c.Assert(err, IsNil)
	defer db.Session.Services().Remove(bson.M{"_id": "mysql"})
	instance := ServiceInstance{
		Name:        "my-mysql",
		ServiceName: "mysql",
		Teams:       []string{s.team.Name},
		Env:         map[string]string{"DATABASE_NAME": "mymysql", "DATABASE_HOST": "localhost"},
		State:       "running",
	}
	err = instance.Create()
	c.Assert(err, IsNil)
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "my-mysql"})
	a := app.App{
		Name:  "painkiller",
		Teams: []string{s.team.Name},
		Units: []unit.Unit{unit.Unit{Ip: "127.0.0.1"}},
	}
	err = a.Create()
	c.Assert(err, IsNil)
	defer a.Destroy()
	url := fmt.Sprintf("/services/instances/%s/%s?:instance=%s&:app=%s", instance.Name, a.Name, instance.Name, a.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = BindHandler(recorder, request, s.user)
	c.Assert(err, IsNil)
	err = db.Session.Apps().Find(bson.M{"name": a.Name}).One(&a)
	c.Assert(err, IsNil)
	expectedEnv := map[string]app.EnvVar{
		"DATABASE_NAME": app.EnvVar{
			Name:         "DATABASE_NAME",
			Value:        "mymysql",
			Public:       false,
			InstanceName: instance.Name,
		},
		"DATABASE_HOST": app.EnvVar{
			Name:         "DATABASE_HOST",
			Value:        "localhost",
			Public:       false,
			InstanceName: instance.Name,
		},
		"DATABASE_USER": app.EnvVar{
			Name:         "DATABASE_USER",
			Value:        "root",
			Public:       false,
			InstanceName: instance.Name,
		},
		"DATABASE_PASSWORD": app.EnvVar{
			Name:         "DATABASE_PASSWORD",
			Value:        "s3cr3t",
			Public:       false,
			InstanceName: instance.Name,
		},
	}
	c.Assert(a.Env, DeepEquals, expectedEnv)
}

func (s *S) TestBindHandlerReturns404IfTheInstanceDoesNotExist(c *C) {
	a := app.App{Name: "serviceApp", Framework: "django", Teams: []string{s.team.Name}}
	err := a.Create()
	c.Assert(err, IsNil)
	defer a.Destroy()
	url := fmt.Sprintf("/services/instances/unknown/%s?:instance=unknown&:app=%s", a.Name, a.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = BindHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusNotFound)
	c.Assert(e, ErrorMatches, "^Instance not found$")
}

func (s *S) TestBindHandlerReturns403IfTheUserDoesNotHaveAccessToTheInstance(c *C) {
	instance := ServiceInstance{Name: "my-mysql", ServiceName: "mysql", State: "running"}
	err := instance.Create()
	c.Assert(err, IsNil)
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "my-mysql"})
	a := app.App{Name: "serviceApp", Framework: "django", Teams: []string{s.team.Name}}
	err = a.Create()
	c.Assert(err, IsNil)
	defer a.Destroy()
	url := fmt.Sprintf("/services/instances/%s/%s?:instance=%s&:app=%s", instance.Name, a.Name, instance.Name, a.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = BindHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusForbidden)
	c.Assert(e, ErrorMatches, "^This user does not have access to this instance$")
}

func (s *S) TestBindHandlerReturns412IfTheInstanceIsNotCreatedYet(c *C) {
	instance := ServiceInstance{Name: "my-mysql", ServiceName: "mysql", Teams: []string{s.team.Name}}
	err := instance.Create()
	c.Assert(err, IsNil)
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "my-mysql"})
	a := app.App{Name: "painkiller", Teams: []string{s.team.Name}}
	err = a.Create()
	c.Assert(err, IsNil)
	defer a.Destroy()
	url := fmt.Sprintf("/services/instances/%s/%s?:instance=%s&:app=%s", instance.Name, a.Name, instance.Name, a.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = BindHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusPreconditionFailed)
	c.Assert(e, ErrorMatches, "^This service instance is not ready yet.$")
}

func (s *S) TestBindHandlerReturns404IfTheAppDoesNotExist(c *C) {
	instance := ServiceInstance{Name: "my-mysql", ServiceName: "mysql", Teams: []string{s.team.Name}, State: "running"}
	err := instance.Create()
	c.Assert(err, IsNil)
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "my-mysql"})
	url := fmt.Sprintf("/services/instances/%s/unknown?:instance=%s&app=unknown", instance.Name, instance.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = BindHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusNotFound)
	c.Assert(e, ErrorMatches, "^App not found$")
}

func (s *S) TestBindHandlerReturns403IfTheUserDoesNotHaveAccessToTheApp(c *C) {
	instance := ServiceInstance{Name: "my-mysql", ServiceName: "mysql", Teams: []string{s.team.Name}, State: "running"}
	err := instance.Create()
	c.Assert(err, IsNil)
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "my-mysql"})
	a := app.App{Name: "serviceApp", Framework: "django"}
	err = a.Create()
	c.Assert(err, IsNil)
	defer a.Destroy()
	url := fmt.Sprintf("/services/instances/%s/%s?:instance=%s&:app=%s", instance.Name, a.Name, instance.Name, a.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = BindHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusForbidden)
	c.Assert(e, ErrorMatches, "^This user does not have access to this app$")
}

func (s *S) TestBindHandlerReturns412IfTheAppDoesNotHaveAnUnitAndServiceHasEndpoint(c *C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"DATABASE_USER":"root","DATABASE_PASSWORD":"s3cr3t"}`))
	}))
	defer ts.Close()
	service := Service{Name: "mysql", Endpoint: map[string]string{"production": ts.URL}}
	err := service.Create()
	c.Assert(err, IsNil)
	defer db.Session.Services().Remove(bson.M{"_id": "mysql"})
	instance := ServiceInstance{Name: "my-mysql", ServiceName: "mysql", Teams: []string{s.team.Name}, State: "running"}
	err = instance.Create()
	c.Assert(err, IsNil)
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "my-mysql"})
	a := app.App{Name: "painkiller", Teams: []string{s.team.Name}}
	err = a.Create()
	c.Assert(err, IsNil)
	defer a.Destroy()
	url := fmt.Sprintf("/services/instances/%s/%s?:instance=%s&:app=%s", instance.Name, a.Name, instance.Name, a.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = BindHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusPreconditionFailed)
	c.Assert(e.Message, Equals, "This app does not have an IP yet.")
}

func (s *S) TestBindHandlerReturns409IfTheAppIsAlreadyBindedToTheService(c *C) {
	service := Service{Name: "mysql"}
	err := service.Create()
	c.Assert(err, IsNil)
	defer db.Session.Services().Remove(bson.M{"_id": "mysql"})
	instance := ServiceInstance{
		Name:        "my-mysql",
		ServiceName: "mysql",
		Teams:       []string{s.team.Name},
		Apps:        []string{"painkiller"},
		State:       "running",
	}
	err = instance.Create()
	c.Assert(err, IsNil)
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "my-mysql"})
	a := app.App{Name: "painkiller", Teams: []string{s.team.Name}}
	err = a.Create()
	c.Assert(err, IsNil)
	defer a.Destroy()
	url := fmt.Sprintf("/services/instances/%s/%s?:instance=%s&:app=%s", instance.Name, a.Name, instance.Name, a.Name)
	req, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = BindHandler(recorder, req, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusConflict)
	c.Assert(e, ErrorMatches, "^This app is already binded to this service instance.$")
}

func (s *S) TestUnbindHandlerRemovesAppFromServiceInstance(c *C) {
	service := Service{Name: "mysql"}
	err := service.Create()
	c.Assert(err, IsNil)
	defer db.Session.Services().Remove(bson.M{"_id": "mysql"})
	instance := ServiceInstance{
		Name:        "my-mysql",
		ServiceName: "mysql",
		Teams:       []string{s.team.Name},
		Apps:        []string{"painkiller"},
		State:       "running",
	}
	err = instance.Create()
	c.Assert(err, IsNil)
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "my-mysql"})
	a := app.App{Name: "painkiller", Teams: []string{s.team.Name}}
	err = a.Create()
	c.Assert(err, IsNil)
	defer a.Destroy()
	url := fmt.Sprintf("/services/instances/%s/%s?:instance=%s&:app=%s", instance.Name, a.Name, instance.Name, a.Name)
	req, err := http.NewRequest("DELETE", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = UnbindHandler(recorder, req, s.user)
	c.Assert(err, IsNil)
	err = db.Session.ServiceInstances().Find(bson.M{"_id": instance.Name}).One(&instance)
	c.Assert(err, IsNil)
	c.Assert(instance.Apps, DeepEquals, []string{})
}

func (s *S) TestUnbindHandlerRemovesEnvironmentVariableFromApp(c *C) {
	service := Service{Name: "mysql"}
	err := service.Create()
	c.Assert(err, IsNil)
	defer db.Session.Services().Remove(bson.M{"_id": "mysql"})
	instance := ServiceInstance{
		Name:        "my-mysql",
		ServiceName: "mysql",
		Teams:       []string{s.team.Name},
		Apps:        []string{"painkiller"},
		State:       "running",
	}
	err = instance.Create()
	c.Assert(err, IsNil)
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "my-mysql"})
	a := app.App{
		Name:  "painkiller",
		Teams: []string{s.team.Name},
		Env: map[string]app.EnvVar{
			"DATABASE_HOST": app.EnvVar{
				Name:         "DATABASE_HOST",
				Value:        "arrea",
				Public:       false,
				InstanceName: instance.Name,
			},
			"MY_VAR": app.EnvVar{
				Name:  "MY_VAR",
				Value: "123",
			},
		},
	}
	err = a.Create()
	c.Assert(err, IsNil)
	defer a.Destroy()
	url := fmt.Sprintf("/services/instances/%s/%s?:instance=%s&:app=%s", instance.Name, a.Name, instance.Name, a.Name)
	req, err := http.NewRequest("DELETE", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = UnbindHandler(recorder, req, s.user)
	c.Assert(err, IsNil)
	err = db.Session.Apps().Find(bson.M{"name": a.Name}).One(&a)
	expected := map[string]app.EnvVar{
		"MY_VAR": app.EnvVar{
			Name:  "MY_VAR",
			Value: "123",
		},
	}
	c.Assert(a.Env, DeepEquals, expected)
}

func (s *S) TestUnbindHandlerCallsTheUnbindMethodFromAPI(c *C) {
	var called bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = r.Method == "DELETE" && r.URL.Path == "/resources/my-mysql/hostname/127.0.0.1/"
	}))
	defer ts.Close()
	service := Service{Name: "mysql", Endpoint: map[string]string{"production": ts.URL}}
	err := service.Create()
	c.Assert(err, IsNil)
	instance := ServiceInstance{
		Name:        "my-mysql",
		ServiceName: "mysql",
		Teams:       []string{s.team.Name},
		Apps:        []string{"painkiller"},
		State:       "running",
	}
	err = instance.Create()
	c.Assert(err, IsNil)
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "my-mysql"})
	a := app.App{
		Name:  "painkiller",
		Teams: []string{s.team.Name},
		Units: []unit.Unit{unit.Unit{Ip: "127.0.0.1"}},
	}
	err = a.Create()
	c.Assert(err, IsNil)
	defer a.Destroy()
	url := fmt.Sprintf("/services/instances/%s/%s?:instance=%s&:app=%s", instance.Name, a.Name, instance.Name, a.Name)
	req, err := http.NewRequest("DELETE", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = UnbindHandler(recorder, req, s.user)
	c.Assert(err, IsNil)
	ch := make(chan bool)
	go func() {
		t := time.Tick(1)
		for _ = <-t; !called; _ = <-t {
		}
		ch <- true
	}()
	select {
	case <-ch:
		c.SucceedNow()
	case <-time.After(1e9):
		c.Errorf("Failed to call API after 1 second.")
	}
}

func (s *S) TestUnbindHandlerReturns404IfTheInstanceDoesNotExist(c *C) {
	a := app.App{Name: "serviceApp", Framework: "django", Teams: []string{s.team.Name}}
	err := a.Create()
	c.Assert(err, IsNil)
	defer a.Destroy()
	url := fmt.Sprintf("/services/instances/unknown/%s?:instance=unknown&:app=%s", a.Name, a.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = UnbindHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusNotFound)
	c.Assert(e, ErrorMatches, "^Instance not found$")
}

func (s *S) TestUnbindHandlerReturns403IfTheUserDoesNotHaveAccessToTheInstance(c *C) {
	instance := ServiceInstance{Name: "my-mysql", ServiceName: "mysql", State: "running"}
	err := instance.Create()
	c.Assert(err, IsNil)
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "my-mysql"})
	a := app.App{Name: "serviceApp", Framework: "django", Teams: []string{s.team.Name}}
	err = a.Create()
	c.Assert(err, IsNil)
	defer a.Destroy()
	url := fmt.Sprintf("/services/instances/%s/%s?:instance=%s&:app=%s", instance.Name, a.Name, instance.Name, a.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = UnbindHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusForbidden)
	c.Assert(e, ErrorMatches, "^This user does not have access to this instance$")
}

func (s *S) TestUnbindHandlerReturns404IfTheAppDoesNotExist(c *C) {
	instance := ServiceInstance{Name: "my-mysql", ServiceName: "mysql", Teams: []string{s.team.Name}, State: "running"}
	err := instance.Create()
	c.Assert(err, IsNil)
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "my-mysql"})
	url := fmt.Sprintf("/services/instances/%s/unknown?:instance=%s&app=unknown", instance.Name, instance.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = UnbindHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusNotFound)
	c.Assert(e, ErrorMatches, "^App not found$")
}

func (s *S) TestUnbindHandlerReturns403IfTheUserDoesNotHaveAccessToTheApp(c *C) {
	instance := ServiceInstance{Name: "my-mysql", ServiceName: "mysql", Teams: []string{s.team.Name}, State: "running"}
	err := instance.Create()
	c.Assert(err, IsNil)
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "my-mysql"})
	a := app.App{Name: "serviceApp", Framework: "django"}
	err = a.Create()
	c.Assert(err, IsNil)
	defer a.Destroy()
	url := fmt.Sprintf("/services/instances/%s/%s?:instance=%s&:app=%s", instance.Name, a.Name, instance.Name, a.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = UnbindHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusForbidden)
	c.Assert(e, ErrorMatches, "^This user does not have access to this app$")
}

func (s *S) TestUnbindHandlerReturns412IfTheAppIsNotBindedToTheInstance(c *C) {
	instance := ServiceInstance{Name: "my-mysql", ServiceName: "mysql", Teams: []string{s.team.Name}, State: "running"}
	err := instance.Create()
	c.Assert(err, IsNil)
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "my-mysql"})
	a := app.App{Name: "serviceApp", Framework: "django", Teams: []string{s.team.Name}}
	err = a.Create()
	c.Assert(err, IsNil)
	defer a.Destroy()
	url := fmt.Sprintf("/services/instances/%s/%s?:instance=%s&:app=%s", instance.Name, a.Name, instance.Name, a.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = UnbindHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusPreconditionFailed)
	c.Assert(e, ErrorMatches, "^This app is not binded to this service instance.$")
}

func (s *S) TestUnbindServiceInstanceFromAppReturnsErrorIfTheGivenArgumentIsNotAnApp(c *C) {
	inputList := []interface{}{
		"something",
		app.App{},
	}
	expectedList := []error{
		stderrors.New("app must have type app.App"),
		nil,
	}
	for i, input := range inputList {
		expected := expectedList[i]
		c.Assert(UnbindServiceInstancesFromApp(input), DeepEquals, expected)
	}
}

func (s *S) TestUnbindServiceInstancesFromAppUnbindsAppByItsInstance(c *C) {
	service := Service{Name: "mysql"}
	err := service.Create()
	c.Assert(err, IsNil)
	instance := ServiceInstance{
		Name:        "my-mysql",
		ServiceName: "mysql",
		Teams:       []string{s.team.Name},
		Apps:        []string{"painkiller"},
		State:       "running",
	}
	err = instance.Create()
	c.Assert(err, IsNil)
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "my-mysql"})
	instance2 := ServiceInstance{
		Name:        "other-mysql",
		ServiceName: "mysql",
		Teams:       []string{s.team.Name},
		Apps:        []string{"painkiller"},
		State:       "running",
	}
	err = instance2.Create()
	c.Assert(err, IsNil)
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "other-mysql"})
	a := app.App{
		Name:  "painkiller",
		Teams: []string{s.team.Name},
		Units: []unit.Unit{unit.Unit{Ip: "127.0.0.1"}},
	}
	err = a.Create()
	c.Assert(err, IsNil)
	defer a.Destroy()
	err = UnbindServiceInstancesFromApp(a)
	c.Assert(err, IsNil)
	n, err := db.Session.ServiceInstances().Find(bson.M{"apps": bson.M{"$in": []string{a.Name}}}).Count()
	c.Assert(err, IsNil)
	c.Assert(n, Equals, 0)
}

func (s *S) TestGrantAccessToTeam(c *C) {
	t := &auth.Team{Name: "blaaaa"}
	db.Session.Teams().Insert(t)
	defer db.Session.Teams().Remove(bson.M{"name": t.Name})
	se := Service{Name: "my_service", Teams: []string{s.team.Name}}
	err := se.Create()
	defer se.Delete()
	c.Assert(err, IsNil)
	url := fmt.Sprintf("/services/%s/%s?:service=%s&:team=%s", se.Name, t.Name, se.Name, t.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = GrantAccessToTeamHandler(recorder, request, s.user)
	c.Assert(err, IsNil)
	err = se.Get()
	c.Assert(err, IsNil)
	c.Assert(*s.team, HasAccessTo, se)
}

func (s *S) TestGrantAccesToTeamReturnNotFoundIfTheServiceDoesNotExist(c *C) {
	url := fmt.Sprintf("/services/nononono/%s?:service=nononono&:team=%s", s.team.Name, s.team.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = GrantAccessToTeamHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusNotFound)
	c.Assert(e, ErrorMatches, "^Service not found$")
}

func (s *S) TestGrantAccessToTeamReturnForbiddenIfTheGivenUserDoesNotHaveAccessToTheService(c *C) {
	se := Service{Name: "my_service"}
	err := se.Create()
	c.Assert(err, IsNil)
	defer se.Delete()
	url := fmt.Sprintf("/services/%s/%s?:service=%s&:team=%s", se.Name, s.team.Name, se.Name, s.team.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = GrantAccessToTeamHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusForbidden)
	c.Assert(e, ErrorMatches, "^This user does not have access to this service$")
}

func (s *S) TestGrantAccessToTeamReturnNotFoundIfTheTeamDoesNotExist(c *C) {
	se := Service{Name: "my_service", Teams: []string{s.team.Name}}
	err := se.Create()
	c.Assert(err, IsNil)
	defer se.Delete()
	url := fmt.Sprintf("/services/%s/nonono?:service=%s&:team=nonono", se.Name, se.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = GrantAccessToTeamHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusNotFound)
	c.Assert(e, ErrorMatches, "^Team not found$")
}

func (s *S) TestGrantAccessToTeamReturnConflictIfTheTeamAlreadyHasAccessToTheService(c *C) {
	se := Service{Name: "my_service", Teams: []string{s.team.Name}}
	err := se.Create()
	defer se.Delete()
	c.Assert(err, IsNil)
	url := fmt.Sprintf("/services/%s/%s?:service=%s&:team=%s", se.Name, s.team.Name, se.Name, s.team.Name)
	request, err := http.NewRequest("PUT", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = GrantAccessToTeamHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusConflict)
}

func (s *S) TestRevokeAccessFromTeamRemovesTeamFromService(c *C) {
	t := &auth.Team{Name: "alle-da"}
	se := Service{Name: "my_service", Teams: []string{s.team.Name, t.Name}}
	err := se.Create()
	c.Assert(err, IsNil)
	defer se.Delete()
	url := fmt.Sprintf("/services/%s/%s?:service=%s&:team=%s", se.Name, s.team.Name, se.Name, s.team.Name)
	request, err := http.NewRequest("DELETE", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = RevokeAccessFromTeamHandler(recorder, request, s.user)
	c.Assert(err, IsNil)
	err = se.Get()
	c.Assert(err, IsNil)
	c.Assert(*s.team, Not(HasAccessTo), se)
}

func (s *S) TestRevokeAccessFromTeamReturnsNotFoundIfTheServiceDoesNotExist(c *C) {
	url := fmt.Sprintf("/services/nonono/%s?:service=nonono&:team=%s", s.team.Name, s.team.Name)
	request, err := http.NewRequest("DELETE", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = RevokeAccessFromTeamHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusNotFound)
	c.Assert(e, ErrorMatches, "^Service not found$")
}

func (s *S) TestRevokeAccesFromTeamReturnsForbiddenIfTheGivenUserDoesNotHasAccessToTheService(c *C) {
	t := &auth.Team{Name: "alle-da"}
	se := Service{Name: "my_service", Teams: []string{t.Name}}
	err := se.Create()
	c.Assert(err, IsNil)
	defer se.Delete()
	url := fmt.Sprintf("/services/%s/%s?:service=%s&:team=%s", se.Name, t.Name, se.Name, t.Name)
	request, err := http.NewRequest("DELETE", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = RevokeAccessFromTeamHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusForbidden)
	c.Assert(e, ErrorMatches, "^This user does not have access to this service$")
}

func (s *S) TestRevokeAccessFromTeamReturnsNotFoundIfTheTeamDoesNotExist(c *C) {
	se := Service{Name: "my_service", Teams: []string{s.team.Name}}
	err := se.Create()
	c.Assert(err, IsNil)
	defer se.Delete()
	url := fmt.Sprintf("/services/%s/nonono?:service=%s&:team=nonono", se.Name, se.Name)
	request, err := http.NewRequest("DELETE", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = RevokeAccessFromTeamHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusNotFound)
	c.Assert(e, ErrorMatches, "^Team not found$")
}

func (s *S) TestRevokeAccessFromTeamReturnsForbiddenIfTheTeamIsTheOnlyWithAccessToTheService(c *C) {
	se := Service{Name: "my_service", Teams: []string{s.team.Name}}
	err := se.Create()
	c.Assert(err, IsNil)
	defer se.Delete()
	url := fmt.Sprintf("/services/%s/%s?:service=%s&:team=%s", se.Name, s.team.Name, se.Name, s.team.Name)
	request, err := http.NewRequest("DELETE", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = RevokeAccessFromTeamHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusForbidden)
	c.Assert(e, ErrorMatches, "^You can not revoke the access from this team, because it is the unique team with access to this service, and a service can not be orphaned$")
}

func (s *S) TestRevokeAccessFromTeamReturnNotFoundIfTheTeamDoesNotHasAccessToTheService(c *C) {
	t := &auth.Team{Name: "Rammlied"}
	db.Session.Teams().Insert(t)
	defer db.Session.Teams().RemoveAll(bson.M{"name": t.Name})
	se := Service{Name: "my_service", Teams: []string{s.team.Name, s.team.Name}}
	err := se.Create()
	c.Assert(err, IsNil)
	defer se.Delete()
	url := fmt.Sprintf("/services/%s/%s?:service=%s&:team=%s", se.Name, t.Name, se.Name, t.Name)
	request, err := http.NewRequest("DELETE", url, nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = RevokeAccessFromTeamHandler(recorder, request, s.user)
	c.Assert(err, NotNil)
	e, ok := err.(*errors.Http)
	c.Assert(ok, Equals, true)
	c.Assert(e.Code, Equals, http.StatusNotFound)
}

func (s *S) TestServicesHandler(c *C) {
	app := app.App{Name: "globo", Teams: []string{s.team.Name}}
	err := app.Create()
	c.Assert(err, IsNil)
	service := Service{Name: "redis", Teams: []string{s.team.Name}}
	err = service.Create()
	c.Assert(err, IsNil)
	instance := ServiceInstance{
		Name:        "redis-globo",
		ServiceName: "redis",
		Apps:        []string{"globo"},
		Teams:       []string{s.team.Name},
	}
	err = instance.Create()
	c.Assert(err, IsNil)
	request, err := http.NewRequest("GET", "/services/instances", nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = ServicesHandler(recorder, request, s.user)
	c.Assert(err, IsNil)
	body, err := ioutil.ReadAll(recorder.Body)
	c.Assert(err, IsNil)
	var instances map[string][]string
	err = json.Unmarshal(body, &instances)
	c.Assert(err, IsNil)
	expected := map[string][]string{
		"redis": []string{"redis-globo"},
	}
	c.Assert(instances, DeepEquals, expected)
}

func (s *S) TestServicesHandlerReturnsOnlyServicesThatTheUserHasAccess(c *C) {
	u := &auth.User{Email: "me@globo.com", Password: "123"}
	err := u.Create()
	c.Assert(err, IsNil)
	defer db.Session.Users().Remove(bson.M{"email": u.Email})
	app := app.App{Name: "globo", Teams: []string{s.team.Name}}
	err = app.Create()
	c.Assert(err, IsNil)
	service := Service{Name: "redis"}
	err = service.Create()
	c.Assert(err, IsNil)
	instance := ServiceInstance{
		Name:        "redis-globo",
		ServiceName: "redis",
		Apps:        []string{"globo"},
	}
	err = instance.Create()
	c.Assert(err, IsNil)
	request, err := http.NewRequest("GET", "/services/instances", nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = ServicesHandler(recorder, request, u)
	c.Assert(err, IsNil)
	body, err := ioutil.ReadAll(recorder.Body)
	c.Assert(err, IsNil)
	var instances map[string][]string
	err = json.Unmarshal(body, &instances)
	c.Assert(err, IsNil)
	c.Assert(instances, DeepEquals, map[string][]string(nil))
}

func (s *S) TestServicesHandlerFilterInstancesPerServiceIncludingServicesThatDoesNotHaveInstances(c *C) {
	u := &auth.User{Email: "me@globo.com", Password: "123"}
	err := u.Create()
	c.Assert(err, IsNil)
	defer db.Session.Users().Remove(bson.M{"email": u.Email})
	app := app.App{Name: "globo", Teams: []string{s.team.Name}}
	err = app.Create()
	c.Assert(err, IsNil)
	serviceNames := []string{"redis", "mysql", "pgsql", "memcached"}
	defer db.Session.Services().RemoveAll(bson.M{"name": bson.M{"$in": serviceNames}})
	defer db.Session.ServiceInstances().RemoveAll(bson.M{"service_name": bson.M{"$in": serviceNames}})
	for _, name := range serviceNames {
		service := Service{Name: name, Teams: []string{s.team.Name}}
		err = service.Create()
		c.Assert(err, IsNil)
		instance := ServiceInstance{
			Name:        service.Name + app.Name + "1",
			ServiceName: service.Name,
			Apps:        []string{app.Name},
			Teams:       []string{s.team.Name},
		}
		err = instance.Create()
		c.Assert(err, IsNil)
		instance = ServiceInstance{
			Name:        service.Name + app.Name + "2",
			ServiceName: service.Name,
			Apps:        []string{app.Name},
			Teams:       []string{s.team.Name},
		}
		err = instance.Create()
	}
	service := Service{Name: "oracle", Teams: []string{s.team.Name}}
	err = service.Create()
	c.Assert(err, IsNil)
	defer db.Session.Services().Remove(bson.M{"name": "oracle"})
	request, err := http.NewRequest("GET", "/services/instances", nil)
	c.Assert(err, IsNil)
	recorder := httptest.NewRecorder()
	err = ServicesHandler(recorder, request, s.user)
	c.Assert(err, IsNil)
	body, err := ioutil.ReadAll(recorder.Body)
	c.Assert(err, IsNil)
	var instances map[string][]string
	err = json.Unmarshal(body, &instances)
	c.Assert(err, IsNil)
	expected := map[string][]string{
		"redis":     []string{"redisglobo1", "redisglobo2"},
		"mysql":     []string{"mysqlglobo1", "mysqlglobo2"},
		"pgsql":     []string{"pgsqlglobo1", "pgsqlglobo2"},
		"memcached": []string{"memcachedglobo1", "memcachedglobo2"},
		"oracle":    []string{},
	}
	c.Assert(instances, DeepEquals, expected)
}
