import type { Metadata } from "next";
import ConsoleLayout from "@/components/ConsoleLayout";
import AdminRuntime from "@/components/AdminRuntime";

export const metadata: Metadata = { title: "用户与角色" };

export default function AdminUsersPage() {
  return (
    <ConsoleLayout
      eyebrow="Admin · F1"
      title="用户与角色管理"
      description="注册（邮箱+密码哈希）、登录（JWT access + refresh）、退出、改密。天衡所有用户都是超级用户，没有任何管理员角色；管理面所有 /api/* 仅需 JWT 身份，不做角色判断。"
    >
      <AdminRuntime kind="users" />

      <p className="mt-8 font-mono text-2xs text-muted-foreground">
        POST /api/auth/register · /api/auth/login · /api/auth/refresh · /api/auth/logout · GET /api/me
      </p>
    </ConsoleLayout>
  );
}
