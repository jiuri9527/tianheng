import type { Metadata } from "next";
import Link from "next/link";
import ConsoleLayout from "@/components/ConsoleLayout";
import AvatarUploader from "@/components/AvatarUploader";
import AvatarProfileForm from "@/components/AvatarProfileForm";

export const metadata: Metadata = { title: "个人资料与头像" };

/**
 * /profile —— 用户自己上传头像与个人资料。
 * 服务端页面只负责布局；所有数据由 AvatarUploader / AvatarProfileForm 客户端组件
 * 从 /api/avatar/* 与 /api/profile 真实读取，后端未连接时渲染真实空态。
 */
export default function ProfilePage() {
  return (
    <ConsoleLayout
      eyebrow="Profile · F1 / F12"
      title="个人资料与头像"
      description="上传自己的头像（服务端按真实魔数校验、生成 128×128 缩略图、随机文件名落盘），并维护昵称与简介。所有展示数据来自真实后端，空数据时显示真实空态。"
      actions={
        <Link
          href="/dashboard"
          className="focus-ring rounded-md border border-border bg-card px-4 py-2 text-button font-medium text-foreground no-underline transition-colors duration-quick ease-smooth-out hover:bg-card-hover"
        >
          返回仪表盘
        </Link>
      }
    >
      <div className="grid gap-6 lg:grid-cols-[7fr_5fr]">
        <div className="min-w-0 space-y-6">
          <AvatarUploader />
          <AvatarProfileForm />
        </div>

        <aside className="space-y-5">
          <div className="card-surface p-5">
            <p className="text-overline uppercase tracking-widest text-muted-foreground">端点契约</p>
            <ul className="mt-4 space-y-2.5 font-mono text-2xs leading-relaxed text-muted-foreground">
              <li><span className="text-foreground">POST</span> /api/avatar/upload <span className="text-primary">user</span></li>
              <li><span className="text-foreground">GET</span> /api/avatar/me <span className="text-primary">user</span></li>
              <li><span className="text-foreground">GET</span> /api/avatar/&#123;user_id&#125; <span className="text-positive-text">public · 只返回图片</span></li>
              <li><span className="text-foreground">PUT</span> /api/profile <span className="text-primary">user</span></li>
              <li><span className="text-foreground">GET</span> /api/profile <span className="text-primary">user</span></li>
              <li><span className="text-foreground">GET</span> /api/profile/&#123;user_id&#125; <span className="text-positive-text">public</span></li>
            </ul>
            <p className="mt-4 text-2xs leading-relaxed text-muted-foreground">
              user 端点由 middleware.Chain 包装 RequestID → CORS → BodyLimit → Management(JWT)；
              当前用户由 middleware.UserFrom(r.Context()) 取得，绝不接受请求体里的 user_id。
            </p>
          </div>

          <div className="card-surface p-5">
            <p className="text-overline uppercase tracking-widest text-muted-foreground">安全设计</p>
            <ol className="mt-4 space-y-2.5 text-2xs leading-relaxed text-muted-foreground">
              <li>1. 文件大小 ≤ 2MiB；服务端用 io.LimitReader 多读 1 字节判定超限。</li>
              <li>2. 真实魔数 + Content-Type 双向校验；PNG/JPEG/GIF 还必须通过标准库解码。</li>
              <li>3. 用户上传的原始文件名被丢弃，落盘名为 &lt;userid&gt;.&lt;ext&gt;，扩展名来自白名单。</li>
              <li>4. user_id 用 ^[A-Za-z0-9_-]&#123;1,128&#125;$ 校验，并二次确认路径未逃逸 avatars 目录。</li>
              <li>5. 目录 0700 / 文件 0600，同目录临时文件 + fsync + rename 原子写。</li>
              <li>6. 昵称 ≤ 32 字符、简介 ≤ 280 字符，清控制字符后 html.EscapeString 落盘。</li>
              <li>7. 公开图片端点设 nosniff + CSP sandbox；错误统一走 &#123;"error":&#123;…&#125;&#125; 结构。</li>
              <li>8. 解码前用 DecodeConfig 校验像素总量 ≤ 25MP，防压缩炸弹。</li>
            </ol>
          </div>

          <div className="card-surface p-5">
            <p className="text-overline uppercase tracking-widest text-muted-foreground">数据文件</p>
            <div className="mt-4 space-y-2 font-mono text-2xs text-muted-foreground">
              <p className="text-foreground">cfg.DataDir/avatars/&lt;userid&gt;.&lt;ext&gt;</p>
              <p>cfg.DataDir/avatars/&lt;userid&gt;.thumb.png</p>
              <p>cfg.DataDir/profiles.json</p>
            </div>
            <p className="mt-4 text-2xs leading-relaxed text-muted-foreground">
              profiles.json 不存在时资料表为空；禁止预置任何假资料。头像文件与资料均为用户真实上传/填写后才产生。
            </p>
          </div>
        </aside>
      </div>
    </ConsoleLayout>
  );
}
