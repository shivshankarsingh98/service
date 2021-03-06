// Copyright 2015 Daniel Theophanes.
// Use of this source code is governed by a zlib-style
// license that can be found in the LICENSE file.

package service

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"text/template"	
)

type sysv struct {
	i Interface
	*Config
}

func newSystemVService(i Interface, c *Config) (Service, error) {
	s := &sysv{
		i:      i,
		Config: c,
	}

	return s, nil
}

func (s *sysv) String() string {
	if len(s.DisplayName) > 0 {
		return s.DisplayName
	}
	return s.Name
}

var errNoUserServiceSystemV = errors.New("User services are not supported on SystemV.")

func (s *sysv) configPath() (cp string, err error) {
	if s.Option.bool(optionUserService, optionUserServiceDefault) {
		err = errNoUserServiceSystemV
		return
	}
	cp = "/etc/init.d/" + s.Config.Name
	return
}
func (s *sysv) template() *template.Template {
	return template.Must(template.New("").Funcs(tf).Parse(sysvScript))
}

func (s *sysv) Install() error {
	confPath, err := s.configPath()
	if err != nil {
		return err
	}
	_, err = os.Stat(confPath)
	if err == nil {
		return fmt.Errorf("Init already exists: %s", confPath)
	}

	f, err := os.Create(confPath)
	if err != nil {
		return err
	}
	defer f.Close()

	path, err := s.execPath()
	if err != nil {
		return err
	}

	var to = &struct {
		*Config
		Path string
	}{
		s.Config,
		path,
	}

	err = s.template().Execute(f, to)
	if err != nil {
		return err
	}

	if err = os.Chmod(confPath, 0755); err != nil {
		return err
	}
	if _, err := os.Stat("/sbin/chkconfig"); !os.IsNotExist(err) {
		run("/sbin/chkconfig", "--add", s.Name)
	} else if _, err := os.Stat("/usr/sbin/update-rc.d"); !os.IsNotExist(err) {
		run("/usr/sbin/update-rc.d", s.Name, "defaults")
	} else {
		for _, i := range [...]string{"2", "3", "4", "5"} {
			if err = os.Symlink(confPath, "/etc/rc"+i+".d/S50"+s.Name); err != nil {
				continue
			}
		}
		for _, i := range [...]string{"0", "1", "6"} {
			if err = os.Symlink(confPath, "/etc/rc"+i+".d/K02"+s.Name); err != nil {
				continue
			}
		}
	}

	return nil
}

func (s *sysv) Uninstall() error {
	cp, err := s.configPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat("/sbin/chkconfig"); !os.IsNotExist(err) {
		run("/sbin/chkconfig", "--del", s.Name)
	} else if _, err := os.Stat("/usr/sbin/update-rc.d"); !os.IsNotExist(err) {
		run("/usr/sbin/update-rc.d", "-f", s.Name, "remove")
	} else {
		for _, i := range [...]string{"2", "3", "4", "5"} {
			symlinkPath := "/etc/rc" + i + ".d/S50" + s.Name
			if _, err := os.Lstat(symlinkPath); err == nil {
				os.Remove(symlinkPath)
				continue
			}
		}
		for _, i := range [...]string{"0", "1", "6"} {
			symlinkPath := "/etc/rc" + i + ".d/K02" + s.Name
			if _, err := os.Lstat(symlinkPath); err == nil {
				os.Remove(symlinkPath)
				continue
			}
		}
	}
	if err := os.Remove(cp); err != nil {
		return err
	}	
	//removing old service log files
	os.Remove("/var/log/" + s.Name + ".log")
	os.Remove("/var/log/" + s.Name + ".err")
	//removing service log files
	os.Remove("/var/log/opsramp/" + s.Name + ".log")
	os.Remove("/var/log/opsramp/" + s.Name + ".err")
	return nil
}

func (s *sysv) Logger(errs chan<- error) (Logger, error) {
	if system.Interactive() {
		return ConsoleLogger, nil
	}
	return s.SystemLogger(errs)
}
func (s *sysv) SystemLogger(errs chan<- error) (Logger, error) {
	return newSysLogger(s.Name, errs)
}

func (s *sysv) Run() (err error) {
	err = s.i.Start(s)
	if err != nil {
		return err
	}

	s.Option.funcSingle(optionRunWait, func() {
		var sigChan = make(chan os.Signal, 3)
		signal.Notify(sigChan, syscall.SIGTERM, os.Interrupt)
		<-sigChan
	})()

	return s.i.Stop(s)
}

func (s *sysv) Start() error {
	if os.Getuid() == 0 {
		return run("service", s.Name, "start")
	} else {
		return run("sudo", "-n", "service", s.Name, "start")
	}
}

func (s *sysv) Stop() error {
	if os.Getuid() == 0 {
		return run("service", s.Name, "stop")
	} else {
		return run("sudo", "-n", "service", s.Name, "stop")
	}
}

func (s *sysv) Restart() error {
	if os.Getuid() == 0 {
		return run("service", s.Name, "restart")
	} else {
		return run("sudo", "-n", "service", s.Name, "restart")
	}
	
}

const sysvScript = `#!/bin/sh
# For RedHat and cousins:
# chkconfig: - 99 01
# description: {{.Description}}
# processname: {{.Path}}

### BEGIN INIT INFO
# Provides:          {{.Path}}
# Required-Start:
# Required-Stop:
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: {{.DisplayName}}
# Description:       {{.Description}}
### END INIT INFO

if [ -x /sbin/runuser ]
then
    SU=/sbin/runuser
else
    SU=/bin/su
fi

cmd="{{.Path}}{{range .Arguments}} {{.|cmd}}{{end}}"

name=$(basename $(readlink -f $0))
user={{if .UserName}}"{{.UserName}}"{{end}}
pid_file="/var/run/$name.pid"
stdout_log="/var/log/opsramp/$name.log"
stderr_log="/var/log/opsramp/$name.err"

[ -e /etc/sysconfig/$name ] && . /etc/sysconfig/$name

get_pid() {
    cat "$pid_file"
}

is_running() {
    [ -f "$pid_file" ] && ps $(get_pid) > /dev/null 2>&1
}

case "$1" in
    start)
        if is_running; then
            echo "Already started"
        else
            echo "Starting $name"
            {{if .WorkingDirectory}}cd '{{.WorkingDirectory}}'{{end}}                        
            $SU - $user -s /bin/bash -c "$cmd" >> "$stdout_log" 2>> "$stderr_log" &
            #$cmd >> "$stdout_log" 2>> "$stderr_log" &	   
            #echo $! > "$pid_file"
            ppid=$!
            cpid=""
            for i in $(seq 1 30)
            do
                cpid=$(pgrep -P "$ppid")
                if [ "$cpid" != "" ] ; then
                    break
                fi
                sleep 1                
            done
            if [ "$cpid" != "" ] ; then
                echo $cpid > "$pid_file"
                kill -9 "$ppid"
                wait "$ppid" 2>/dev/null
            else
               echo $ppid > "$pid_file"
            fi
            if ! is_running; then
                echo "Unable to start, see $stdout_log and $stderr_log"
                exit 1
            fi
        fi
    ;;
    stop)
        if is_running; then
            echo -n "Stopping $name.."
            kill $(get_pid)
            for i in $(seq 1 10)
            do
                if ! is_running; then
                    break
                fi
                echo -n "."
                sleep 1
            done
            echo
            if is_running; then
                echo "Not stopped; may still be shutting down or shutdown may have failed"
                exit 1
            else
                echo "Stopped"
                if [ -f "$pid_file" ]; then
                    rm "$pid_file"
                fi
            fi
        else
            echo "Not running"
        fi
    ;;
    restart)
        $0 stop
        if is_running; then
            echo "Unable to stop, will not attempt to start"
            exit 1
        fi
        $0 start
    ;;
    status)
        if is_running; then
            echo "$name Running"
        else
            echo "$name Stopped"
            exit 1
        fi
    ;;
    *)
    echo "Usage: $0 {start|stop|restart|status}"
    exit 1
    ;;
esac
exit 0
`
