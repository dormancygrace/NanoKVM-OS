// SPDX-License-Identifier: AGPL-3.0-only
// Finite loopback-only relay fixture for one coordinated encrypted interop test.
#define _GNU_SOURCE
#include <sys/socket.h>
#include <sys/select.h>
#include <netinet/in.h>
#include <stdint.h>
#include <unistd.h>
#include <fcntl.h>
#include <stdlib.h>
#include <stdio.h>
#include <errno.h>
#include <time.h>
#include <signal.h>
static int exact(int fd,void *buf,size_t n){
 size_t at=0;while(at<n){ssize_t r=read(fd,(char*)buf+at,n-at);if(r<=0)return -1;at+=(size_t)r;}return 0;
}
static int request(int fd){
 uint8_t h[4];if(exact(fd,h,1))return -1;unsigned n=(h[0]&3)+1;
 if(exact(fd,h+1,n-1))return -1;
 uint32_t len=0;for(unsigned i=0;i<n;i++)len|=(uint32_t)h[i]<<(8*i);len>>=2;
 if(len>65536)return -1;
 uint8_t *b=malloc(len?len:1);if(!b)return -1;int r=exact(fd,b,len);free(b);return r;
}
int main(void){
 signal(SIGPIPE,SIG_IGN);alarm(40);
 int l=socket(AF_INET,SOCK_STREAM|SOCK_CLOEXEC,0),one=1;
 setsockopt(l,SOL_SOCKET,SO_REUSEADDR,&one,sizeof(one));
 struct sockaddr_in sa={.sin_family=AF_INET,.sin_port=htons(32317),.sin_addr={.s_addr=htonl(INADDR_LOOPBACK)}};
 if(l<0||bind(l,(void*)&sa,sizeof(sa))||listen(l,2))return 1;
 puts("ready");fflush(stdout);
 int fd[2];for(unsigned i=0;i<2;i++){fd[i]=accept4(l,0,0,SOCK_CLOEXEC);if(fd[i]<0||fd[i]>=FD_SETSIZE||request(fd[i]))return 2;}
 close(l);for(unsigned i=0;i<2;i++)if(fcntl(fd[i],F_SETFL,O_NONBLOCK)<0)return 2;
 uint8_t *buf[2]={malloc(65536),malloc(65536)};size_t used[2]={0},sent[2]={0};uint64_t total[2]={0};
 if(!buf[0]||!buf[1])return 2;
 time_t until=time(0)+30;
 while(time(0)<until){
  fd_set rd,wr;FD_ZERO(&rd);FD_ZERO(&wr);
  for(unsigned i=0;i<2;i++){if(!used[i])FD_SET(fd[i],&rd);if(used[i]>sent[i])FD_SET(fd[1-i],&wr);}
  struct timeval wait={.tv_sec=1};int max=fd[0]>fd[1]?fd[0]:fd[1],r=select(max+1,&rd,&wr,0,&wait);
  if(r<0&&errno==EINTR)continue;
  if(r<0)return 3;
  for(unsigned i=0;i<2;i++){
   if(FD_ISSET(fd[i],&rd)){ssize_t n=read(fd[i],buf[i],65536);if(n==0)goto done;if(n>0){used[i]=(size_t)n;sent[i]=0;}else if(errno!=EAGAIN&&errno!=EINTR)goto done;}
   if(FD_ISSET(fd[1-i],&wr)){ssize_t n=write(fd[1-i],buf[i]+sent[i],used[i]-sent[i]);if(n>0){sent[i]+=(size_t)n;total[i]+=(uint64_t)n;if(sent[i]==used[i])used[i]=sent[i]=0;}else if(n<0&&errno!=EAGAIN&&errno!=EINTR)goto done;}
  }
 }
done:
 close(fd[0]);close(fd[1]);free(buf[0]);free(buf[1]);
 printf("finite bytes %llu %llu\n",(unsigned long long)total[0],(unsigned long long)total[1]);return 0;
}
