#define _GNU_SOURCE
#include <sys/ptrace.h>
#include <sys/wait.h>
#include <stdint.h>
#include <unistd.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <time.h>
struct info { uint8_t op,pad[3];uint32_t arch;uint64_t ip,sp,nr,args[6]; };
static uint64_t ns(void){struct timespec t;if(clock_gettime(CLOCK_MONOTONIC,&t))abort();return (uint64_t)t.tv_sec*1000000000+t.tv_nsec;}
int main(int argc,char **argv){if(argc<2)return 2;pid_t p=fork();if(!p){if(ptrace(PTRACE_TRACEME,0,0,0)<0)_exit(3);raise(SIGSTOP);execvp(argv[1],argv+1);_exit(4);}int st;waitpid(p,&st,0);if(ptrace(PTRACE_SETOPTIONS,p,0,PTRACE_O_TRACESYSGOOD|PTRACE_O_EXITKILL)<0)return 5;int sig=0;uint64_t start=0,nr=0,arg1=0;unsigned long counts[512]={0};
for(;;){if(ptrace(PTRACE_SYSCALL,p,0,sig)<0)return 6;if(waitpid(p,&st,0)<0)return 7;if(WIFEXITED(st)||WIFSIGNALED(st))break;sig=WSTOPSIG(st);if(sig==(SIGTRAP|0x80)){struct info i={0};if(ptrace(0x420e,p,sizeof i,&i)<0)return 8;if(i.op==1){nr=i.nr;arg1=i.args[1];start=ns();if(nr<512)counts[nr]++;}else if(i.op==2){uint64_t elapsed=ns()-start;if(nr==29||nr==56||nr==57||nr==79||nr==80||nr==291)fprintf(stderr,"{\"syscall\":%llu,\"ioctl_request\":%llu,\"result\":%lld,\"traced_wall_ns\":%llu}\n",(unsigned long long)nr,(unsigned long long)(nr==29?arg1:0),(long long)i.nr,(unsigned long long)elapsed);}sig=0;}else if(sig==SIGTRAP)sig=0;}
fputs("{\"counts\":{",stderr);int sep=0;for(unsigned i=0;i<512;i++)if(counts[i]){fprintf(stderr,"%s\"%u\":%lu",sep?",":"",i,counts[i]);sep=1;}fputs("}}\n",stderr);return WIFEXITED(st)?WEXITSTATUS(st):128+WTERMSIG(st);}
