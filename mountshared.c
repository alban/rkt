#include <sys/mount.h>

int main()
{
	return mount("none", "/", "none", MS_SHARED, "");
}

