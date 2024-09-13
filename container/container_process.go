package container

import (
	log "github.com/sirupsen/logrus"
	"os"
	"os/exec"
	"syscall"
)

func StartContainer(tty bool, cmd string) {
	/**
	该方法构建command命令，用于接下来fork新进程，该进程主要设置隔离的namespace
	比如客户端调用：./dockerv run -it /bin/sh，这里command就是：/proc/self/exe init /bin/sh，
	发送init指令，接下来走initCommand流程

	注意：/proc/self/exe就是调用自己
	*/
	parent := createInitProcess(tty, cmd)
	//调用command，fork新进程
	if err := parent.Start(); err != nil {
		log.Error(err)
	}
	log.Info("parent.Wait() begin-----------", os.Getpid())
	//这里阻塞，直到子进程退出才会继续往下走
	_ = parent.Wait()
	log.Info("parent.Wait() end-----------", os.Getpid())
	//子进程退出，当前进程也终止
	os.Exit(-1)
}

/*
构建init进程command，用于后续fork一个init进程：
1./proc/self/exe 指当前进程，这里即为调用dockerv创建出新进程
2.args指定传递参数，其中init是传递给fork进程的第一个参数，即执行init指令，走initCommand流程
3.clone参数指定使用隔离的namespace环境fork出来一个新进程，实现和外部环境隔离
4.如果用户指定了-it参数即为前台运行，就需要把fork新进程的输入输出对接到标准输入输出上
*/
func createInitProcess(tty bool, command string) *exec.Cmd {
	args := []string{"init", command} // 这里的 init 指令就用用来在子进程中调用 initCommand
	cmd := exec.Command("/proc/self/exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS |
			syscall.CLONE_NEWNET | syscall.CLONE_NEWIPC,
	}
	if tty {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd
}

func RunContainerInitProcess(command string, args []string) error {
	// systemd 加入linux之后, mount namespace 就变成 shared by default, 所以你必须显示声明你要这个新的mount namespace独立。
	// 即 mount proc 之前先把所有挂载点的传播类型改为 private，避免本 namespace 中的挂载事件外泄。
	// mount --make-private /
	syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, "")
	/**
	执行 mount -t proc proc /proc 命令重新挂载：
	MS_NOEXEC等是挂载文件系统的一些限制条件，
		MS_NOEXEC：在本文件系统中不允许运行其他程序。
		MS_NOSUID：在本系统中运行程序的时候， 不允许 set-user-ID 或 set-group-ID 。
		MS_NODEV：自 从 Linux 2.4 以来，所有 mount 的系统都会默认设定的参数。
	*/
	defaultMountFlags := syscall.MS_NOEXEC | syscall.MS_NOSUID | syscall.MS_NODEV
	//重新挂载/proc：mount -t proc proc /proc
	//不然在容器里执行 ps -ef，查看到的是宿主机上所有进程
	_ = syscall.Mount("proc", "/proc", "proc", uintptr(defaultMountFlags), "")
	argv := []string{command}
	log.Info("syscall.Exec-------执行开始")
	//syscall.Exec这里会阻塞，除非程序运行完成退出
	/**
	关键的就是syscall.Exec(cmd, argv, os.Environ())这句代码，这句代码执行了一次系统调用，调用内核int execve()这个函数，作用是执行我们指定的程序，而将当前进程原来的信息（镜像、数据、堆栈、PID）覆盖掉。
	那有什么效果呢？
	我们启动容器会通过docker run -it /bin/sh类似的命令进行启动，后面的/bin/sh是我们执行的程序，那么/bin/sh的进程才应该是pid=1的进程，执行完上面的系统调用后，就可以达到这个效果。
	还有另外一个原因是：我们指定的用户进程创建完成后就无法对其进行文件挂载了，所以在执行系统调用前是主进程可管控的子进程，可以对其进行文件挂载和资源限制，限制完成后执行系统调用将其转换为用户进程。
	类似于创建一个参数设定好的子进程，创建完成后再退位给用户进程，起到一个代理进程的作用。
	*/
	if err := syscall.Exec(command, argv, os.Environ()); err != nil {
		log.Errorf(err.Error())
	}
	log.Info("syscall.Exec-------执行完成")
	return nil
}
