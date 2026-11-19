import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

// The address of a link the chat may follow: an absolute http(s) URL of
// another host. Text on the host's screen can steer the model, so a link
// into this device's own interface (a page, an API route) is shown as text.
function externalHref(href: string | undefined) {
  if (!href) return undefined;
  try {
    const url = new URL(href, window.location.href);
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return undefined;
    if (url.host === window.location.host || url.hostname === window.location.hostname)
      return undefined;
    return url.href;
  } catch {
    return undefined;
  }
}

type MarkdownContentProps = {
  content?: string;
  className?: string;
};

export const MarkdownContent = ({ content, className }: MarkdownContentProps) => {
  if (!content) {
    return null;
  }

  return (
    <div className={className}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          // Model and tool output can be steered by text on the host's
          // screen; an image would be fetched at once, with no click, and
          // carry whatever the URL holds. Show its description instead.
          img: ({ alt }) => (alt ? <span className="text-neutral-400">[{alt}]</span> : null),
          p: ({ children }) => (
            <p className="my-2 leading-6 wrap-break-word whitespace-pre-wrap first:mt-0 last:mb-0">
              {children}
            </p>
          ),
          a: ({ children, href }) => {
            const target = externalHref(href);
            return target ? (
              <a
                className="text-sky-300 underline decoration-sky-400/40 underline-offset-2 hover:text-sky-200"
                href={target}
                rel="noreferrer noopener"
                target="_blank"
              >
                {children}
              </a>
            ) : (
              <span className="text-sky-300">{children}</span>
            );
          },
          ul: ({ children }) => <ul className="my-2 list-disc pl-5">{children}</ul>,
          ol: ({ children }) => <ol className="my-2 list-decimal pl-5">{children}</ol>,
          li: ({ children }) => <li className="my-1 leading-6 wrap-break-word">{children}</li>,
          h1: ({ children }) => (
            <h1 className="mt-3 text-base font-semibold first:mt-0">{children}</h1>
          ),
          h2: ({ children }) => (
            <h2 className="mt-3 text-sm font-semibold first:mt-0">{children}</h2>
          ),
          h3: ({ children }) => <h3 className="mt-2 text-sm font-medium first:mt-0">{children}</h3>,
          blockquote: ({ children }) => (
            <blockquote className="border-tint/15 my-2 border-l-2 pl-3 text-neutral-300">
              {children}
            </blockquote>
          ),
          code: ({ children, className: codeClassName, ...props }: any) =>
            !String(codeClassName || '').includes('language-') ? (
              <code
                className="bg-shade/25 rounded px-1.5 py-0.5 font-mono text-[0.92em]"
                {...props}
              >
                {children}
              </code>
            ) : (
              <code className={codeClassName} {...props}>
                {children}
              </code>
            ),
          pre: ({ children }) => (
            <pre className="bg-shade/30 my-2 overflow-x-auto rounded-xl px-3 py-2 text-xs leading-6 text-neutral-100">
              {children}
            </pre>
          ),
          table: ({ children }) => (
            <div className="my-2 overflow-x-auto">
              <table className="w-full border-collapse text-left text-sm">{children}</table>
            </div>
          ),
          th: ({ children }) => (
            <th className="border-tint/10 border-b px-2 py-1 font-semibold">{children}</th>
          ),
          td: ({ children }) => (
            <td className="border-tint/5 border-b px-2 py-1 align-top">{children}</td>
          )
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
};
